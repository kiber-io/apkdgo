package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/kiber-io/apkd/apkd/logging"
	"github.com/kiber-io/apkd/apkd/network"
	"github.com/kiber-io/apkd/apkd/sources"

	"github.com/spf13/cobra"
)

var packageNamesMap = make(map[string]int)
var forceDownload bool
var batchDeveloperDownloadMode bool
var outputDir string
var outputFileName string
var globalProxy string
var proxyInsecureSkipVerify bool
var sourceProxyEntries []string
var packagesFile string
var packageNames []string
var verbosity int
var listSources bool
var printVersion bool

var selectedSources []string
var activeSources []sources.Source

var downloadSuccessCount atomic.Int64
var downloadErrorCount atomic.Int64

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

var rootCmd = cobra.Command{
	Use:   "apkd",
	Short: "apkd is a tool for downloading APKs from multiple sources",
	PreRun: func(cmd *cobra.Command, args []string) {
		if verbosity == 0 {
			verbosity = 1 // default verbosity level
		}
		logging.Init(verbosity)
		// Reset mutable global state to keep repeated in-process runs deterministic.
		packageNamesMap = make(map[string]int)
		activeSources = nil
		if printVersion {
			return
		}

		sourceProxies, err := parseSourceProxyEntries(sourceProxyEntries)
		if err != nil {
			fmt.Printf("Error parsing --source-proxy: %v\n", err)
			os.Exit(1)
		}
		if err := network.ConfigureProxies(globalProxy, sourceProxies, proxyInsecureSkipVerify); err != nil {
			fmt.Printf("Error applying proxy settings: %v\n", err)
			os.Exit(1)
		}

		if err := sources.InitializeRegisteredSources(); err != nil {
			fmt.Printf("Error initializing sources: %v\n", err)
			os.Exit(1)
		}

		if listSources {
			return
		}

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
		if err := validateKnownSources(selectedSources, sourceProxies, allSources); err != nil {
			fmt.Printf("Error validating source names: %v\n", err)
			os.Exit(1)
		}
		if len(selectedSources) > 0 {
			selectedSourcesSet := make(map[string]struct{}, len(selectedSources))
			for _, src := range selectedSources {
				selectedSourcesSet[src] = struct{}{}
			}
			for src := range allSources {
				if _, exists := selectedSourcesSet[src]; exists {
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
		if printVersion {
			fmt.Printf("Version: %s\nCommit: %s\nBuilt at: %s\n", version, commit, buildDate)
		} else if listSources {
			fmt.Println("Available sources:")
			allSources := sources.GetAll()
			for _, src := range allSources {
				fmt.Printf("- %s\n", src.Name())
			}
		} else {
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigChan
				printSummary()
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
			printSummary()
		}
	},
}

func reportError(errText string) {
	downloadErrorCount.Add(1)
	logging.Loge(strings.ReplaceAll(errText, "\n", "\\n"))
}

func reportDownloadSuccess() {
	downloadSuccessCount.Add(1)
}

func printSummary() {
	fmt.Printf("\nSummary: downloaded %d, errors %d\n", downloadSuccessCount.Load(), downloadErrorCount.Load())
}

func parseSourceProxyEntries(entries []string) (map[string]string, error) {
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		normalizedEntry := strings.TrimSpace(entry)
		if normalizedEntry == "" {
			continue
		}
		parts := strings.SplitN(normalizedEntry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid value %q, expected source=proxy-url", entry)
		}
		sourceName := strings.ToLower(strings.TrimSpace(parts[0]))
		proxyURL := strings.TrimSpace(parts[1])
		if sourceName == "" || proxyURL == "" {
			return nil, fmt.Errorf("invalid value %q, source and proxy URL must be non-empty", entry)
		}
		result[sourceName] = proxyURL
	}
	return result, nil
}

func validateKnownSources(selectedSources []string, sourceProxies map[string]string, allSources map[string]sources.Source) error {
	knownSources := make(map[string]struct{}, len(allSources))
	for sourceName := range allSources {
		knownSources[sourceName] = struct{}{}
	}
	unknownSelected := make(map[string]struct{})
	for _, sourceName := range selectedSources {
		if _, exists := knownSources[sourceName]; !exists {
			unknownSelected[sourceName] = struct{}{}
		}
	}
	unknownSourceProxies := make(map[string]struct{})
	for sourceName := range sourceProxies {
		if _, exists := knownSources[sourceName]; !exists {
			unknownSourceProxies[sourceName] = struct{}{}
		}
	}
	if len(unknownSelected) == 0 && len(unknownSourceProxies) == 0 {
		return nil
	}
	parts := make([]string, 0, 2)
	if len(unknownSelected) > 0 {
		parts = append(parts, fmt.Sprintf("--source: %s", joinSortedSet(unknownSelected)))
	}
	if len(unknownSourceProxies) > 0 {
		parts = append(parts, fmt.Sprintf("--source-proxy: %s", joinSortedSet(unknownSourceProxies)))
	}
	return fmt.Errorf("unknown source name(s) in %s. Use --list-sources to see available sources", strings.Join(parts, "; "))
}

func joinSortedSet(values map[string]struct{}) string {
	sortedValues := make([]string, 0, len(values))
	for value := range values {
		sortedValues = append(sortedValues, value)
	}
	sort.Strings(sortedValues)
	return strings.Join(sortedValues, ", ")
}

func main() {
	rootCmd.PersistentFlags().StringArrayVarP(&packageNames, "package", "p", []string{}, "package name of the app")
	rootCmd.PersistentFlags().StringArrayVarP(&selectedSources, "source", "s", []string{}, "specify source(s) for downloading")
	rootCmd.PersistentFlags().StringVar(&globalProxy, "proxy", "", "global proxy URL for all traffic")
	rootCmd.PersistentFlags().BoolVar(&proxyInsecureSkipVerify, "proxy-insecure", false, "skip TLS certificate verification for requests sent through proxy")
	rootCmd.PersistentFlags().StringArrayVar(&sourceProxyEntries, "source-proxy", []string{}, "source proxy mapping in format source=proxy-url (can be repeated)")
	rootCmd.PersistentFlags().StringVarP(&packagesFile, "file", "f", "", "file containing package names")
	rootCmd.PersistentFlags().BoolVarP(&batchDeveloperDownloadMode, "dev", "", false, "download all apps from developer")
	rootCmd.PersistentFlags().BoolVarP(&forceDownload, "force", "F", false, "force download even if the file already exists")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output-dir", "O", "", "output directory for downloaded APKs")
	rootCmd.PersistentFlags().StringVarP(&outputFileName, "output-file", "o", "", "output file name for downloaded APKs")
	rootCmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "Set verbosity level. Use -v or -vv for more verbosity")
	rootCmd.PersistentFlags().BoolVarP(&listSources, "list-sources", "l", false, "list available sources")
	rootCmd.PersistentFlags().BoolVarP(&printVersion, "version", "V", false, "print version and exit")

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
