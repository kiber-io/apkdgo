package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	_ "kiber-io/apkd/apkd/devices"
	"kiber-io/apkd/apkd/sources"

	"slices"

	"github.com/spf13/cobra"
)

var packageNamesMap = make(map[string]int)
var forceDownload bool
var batchDeveloperDownloadMode bool
var outputDir string
var outputFileName string
var packagesFile string
var packageNames []string

var selectedSources []string
var activeSources []sources.Source

var collectedErrors []string

type QueuedVersion struct {
	Version sources.Version
	Source  sources.Source
}

var rootCmd = cobra.Command{
	Use:   "apkd",
	Short: "apkd is a tool for downloading APKs from multiple sources",
	PreRun: func(cmd *cobra.Command, args []string) {
		if packagesFile != "" {
			file, err := os.Open(packagesFile)
			if err != nil {
				fmt.Printf("Error opening file %s: %v\n", packagesFile, err)
				os.Exit(1)
			}
			defer file.Close()

			var packageName string
			for {
				_, err := fmt.Fscanf(file, "%s\n", &packageName)
				if err != nil {
					break
				}
				// support comments
				if strings.HasPrefix(packageName, "#") {
					continue
				}
				packageNames = append(packageNames, packageName)
			}
		}

		for _, pkgName := range packageNames {
			var versionCode int
			if strings.Contains(pkgName, ":") {
				parts := strings.Split(pkgName, ":")
				pkgName = parts[0]
				var err error
				versionCode, err = strconv.Atoi(parts[1])
				if err != nil {
					fmt.Printf("Error parsing version code for package %s: %v\n", pkgName, err)
					os.Exit(1)
				}
			}
			packageNamesMap[pkgName] = versionCode
		}

		if len(packageNamesMap) == 0 {
			fmt.Println("No package names provided. Use --package or --file to specify package names.")
			os.Exit(1)
		}

		for i, src := range selectedSources {
			selectedSources[i] = strings.ToLower(src)
		}
		allSources := sources.GetAll()
		if len(selectedSources) > 0 {
			for src := range allSources {
				if slices.Contains(selectedSources, src) {
					activeSources = append(activeSources, allSources[src])
				}
			}
		} else {
			for src := range allSources {
				activeSources = append(activeSources, allSources[src])
			}
		}
		if len(activeSources) == 0 {
			fmt.Println("No sources available. Please check your sources.")
			os.Exit(1)
		}
		if outputDir != "" {
			outputDir, err, warn := sanitizedAndAbsoluteName(outputDir)
			if err != nil {
				fmt.Printf("Error getting absolute path for output directory %s: %v\n", outputDir, err)
				os.Exit(1)
			}
			if warn != nil {
				fmt.Println("Warning:", warn)
			}
			info, err := os.Stat(outputDir)
			if os.IsNotExist(err) {
				err = os.MkdirAll(outputDir, 0755)
				if err != nil {
					fmt.Printf("Error creating output directory %s: %v\n", outputDir, err)
					os.Exit(1)
				}
			} else if err != nil {
				fmt.Printf("Error checking output directory %s: %v\n", outputDir, err)
				os.Exit(1)
			} else if !info.IsDir() {
				fmt.Printf("Output path %s is not a directory\n", outputDir)
				os.Exit(1)
			}
		}
		if outputFileName != "" {
			if len(packageNamesMap) > 1 {
				fmt.Println("Output file name is not supported when downloading multiple packages.")
				os.Exit(1)
			}
			outputFileName, err, warn := sanitizedAndAbsoluteName(outputFileName)
			if err != nil {
				fmt.Printf("Error getting absolute path for output file %s: %v\n", outputFileName, err)
				os.Exit(1)
			}
			if warn != nil {
				fmt.Println("Warning:", warn)
			}
			if _, err := os.Stat(outputFileName); os.IsExist(err) {
				if !forceDownload {
					fmt.Printf("Output file %s already exists. Use --force to overwrite.\n", outputFileName)
					os.Exit(1)
				}
			}
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigChan
			printCollectedErrors()
			os.Exit(0)
		}()

		tq := NewTaskQueue(3)
		for packageName, versionCode := range packageNamesMap {
			tq.AddTask(PackageTask{
				PackageName: packageName,
				VersionCode: versionCode,
			})
		}

		tq.Wait()
		printCollectedErrors()
	},
}

func printCollectedErrors() {
	if len(collectedErrors) > 0 {
		fmt.Println("\nErrors:")
		for _, err := range collectedErrors {
			fmt.Printf("- %s\n", strings.ReplaceAll(err, "\n", "\\n"))
		}
	}
}

func main() {
	rootCmd.PersistentFlags().StringArrayVarP(&packageNames, "package", "p", []string{}, "package name of the app")
	rootCmd.PersistentFlags().StringArrayVarP(&selectedSources, "source", "s", []string{}, "specify source(s) for downloading")
	rootCmd.PersistentFlags().StringVarP(&packagesFile, "file", "f", "", "file containing package names")
	rootCmd.PersistentFlags().BoolVarP(&batchDeveloperDownloadMode, "dev", "", false, "download all apps from developer")
	rootCmd.PersistentFlags().BoolVarP(&forceDownload, "force", "F", false, "force download even if the file already exists")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output-dir", "O", "", "output directory for downloaded APKs")
	rootCmd.PersistentFlags().StringVarP(&outputFileName, "output-file", "o", "", "output file name for downloaded APKs")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func sanitizeFileName(name string) string {
	reg := regexp.MustCompile(`[<>:"/\\|?*]+`)
	safe := reg.ReplaceAllString(name, "-")
	safe = strings.TrimSpace(safe)
	if len(safe) > 255 {
		safe = safe[:255]
	}

	return safe
}

func sanitizedAndAbsoluteName(name string) (string, error, error) {
	absPath, err := filepath.Abs(name)
	if err != nil {
		return "", err, nil
	}
	base := filepath.Base(absPath)
	sanitizedName := sanitizeFileName(base)
	absPath = filepath.Join(filepath.Dir(absPath), sanitizedName)
	if base != sanitizedName {
		return absPath, nil, fmt.Errorf("name %s is not valid. Using %s instead", base, sanitizedName)
	}
	return absPath, nil, nil
}

func findVersion(packageName string, versionCode int) (sources.Version, sources.Source, []sources.Error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var latestSource sources.Source
	var latestVersion sources.Version
	var appNotFoundError *sources.AppNotFoundError
	var sourcesErrors []sources.Error
	for _, source := range activeSources {
		wg.Add(1)
		go func(src sources.Source) {
			defer wg.Done()
			version, err := src.FindByPackage(packageName, versionCode)
			if err != nil {
				if !errors.As(err, &appNotFoundError) {
					sourcesErrors = append(sourcesErrors, sources.Error{
						SourceName:  src.Name(),
						PackageName: packageName,
						Err:         err,
					})
				}
				return
			}
			mu.Lock()
			if version.Code > latestVersion.Code {
				latestVersion = version
				latestSource = src
			}
			mu.Unlock()
		}(source)
	}

	wg.Wait()

	return latestVersion, latestSource, sourcesErrors
}
