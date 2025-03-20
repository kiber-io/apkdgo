package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "kiber-io/apkd/apkd/devices"
	"kiber-io/apkd/apkd/sources"

	"slices"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

var packageNamesMap = make(map[string]int64)
var forceDownload bool
var batchDeveloperDownloadMode bool
var outputDir string
var outputFileName string
var packagesFile string
var packageNames []string

var selectedSources []string
var activeSources []sources.Source

var pwg sync.WaitGroup
var wg sync.WaitGroup
var sourceLocks = make(map[string]*sync.Mutex)
var sourceCounts = make(map[string]int)
var mu sync.Mutex
var progress = mpb.New(mpb.WithAutoRefresh(), mpb.WithWaitGroup(&pwg))
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
			var versionCode int64
			if strings.Contains(pkgName, ":") {
				parts := strings.Split(pkgName, ":")
				pkgName = parts[0]
				var err error
				versionCode, err = strconv.ParseInt(parts[1], 10, 64)
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
		processPackages(packageNamesMap, false)

		wg.Wait()
		progress.Wait()

		if len(collectedErrors) > 0 {
			fmt.Println("\nErrors:")
			for _, err := range collectedErrors {
				fmt.Printf("- %s\n", err)
			}
		}
	},
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

func findVersion(packageName string, versionCode int64) (sources.Version, sources.Source, []sources.Error) {
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

func showErrorBar(progress *mpb.Progress, prevBar *mpb.Bar, pkgName string, errorText string) {
	prevBar.Abort(false)
	barError := progress.AddBar(1,
		mpb.BarQueueAfter(prevBar),
		mpb.PrependDecorators(
			decor.Name(pkgName, decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(" ["+errorText+"]", decor.WC{C: decor.DSyncSpaceR}),
		),
	)
	barError.IncrBy(1)
}

func processPackages(packageNamesMap map[string]int64, disableBatchDeveloperDownloadMode bool) {
	for packageName, versionCode := range packageNamesMap {
		wg.Add(1)
		go func(pkgName string, versionCode int64) {
			defer wg.Done()
			errs := downloadPackage(pkgName, versionCode, disableBatchDeveloperDownloadMode)
			collectedErrors = append(collectedErrors, errs...)
		}(packageName, versionCode)
	}
}

func downloadPackage(pkgName string, versionCode int64, disableBatchDeveloperDownloadMode bool) []string {
	taskName := pkgName
	if versionCode != 0 {
		taskName = fmt.Sprintf("%s (%d)", pkgName, versionCode)
	}
	barSearch := progress.AddBar(1,
		mpb.PrependDecorators(
			decor.Name(taskName, decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(" [searching]", decor.WC{C: decor.DSyncSpaceR}),
		),
	)

	version, source, errs := findVersion(pkgName, versionCode)
	for _, err := range errs {
		collectedErrors = append(collectedErrors, fmt.Sprintf("Source: %s, Package: %s, Error: %s", err.SourceName, err.PackageName, err.Err.Error()))
	}
	if version == (sources.Version{}) || source == nil {
		var errorText string
		if len(errs) > 0 {
			errorText = "error"
		} else {
			errorText = "not found"
		}
		showErrorBar(progress, barSearch, taskName, errorText)
		return collectedErrors
	}

	if !disableBatchDeveloperDownloadMode && batchDeveloperDownloadMode && version.DeveloperId != "" {
		packages, err := source.FindByDeveloper(version.DeveloperId)
		if err != nil {
			collectedErrors = append(collectedErrors, fmt.Sprintf("Error finding versions by developer %s at source %s: %v", version.DeveloperId, source.Name(), err))
			showErrorBar(progress, barSearch, taskName, "error")
			return collectedErrors
		}
		var newPackages = make(map[string]int64)
		for _, pkg := range packages {
			if _, ok := packageNamesMap[pkg]; !ok {
				newPackages[pkg] = 0
			}
		}
		if len(newPackages) > 0 {
			go processPackages(newPackages, true)
		}
	}
	barWait := progress.AddBar(1,
		mpb.BarQueueAfter(barSearch),
		mpb.PrependDecorators(
			decor.Name(pkgName, decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(fmt.Sprintf("v%s", version.Name), decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(fmt.Sprintf("(%s)", strconv.Itoa(int(version.Code))), decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(source.Name(), decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(" [queued]", decor.WC{C: decor.DSyncSpaceR}),
		),
	)
	// workaround for hang bar: if call IncrBy(1) before BarQueueAfter, it will hang
	barSearch.IncrBy(1)
	bar := progress.AddBar(version.Size,
		mpb.BarQueueAfter(barWait),
		mpb.PrependDecorators(
			decor.Name(pkgName, decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(fmt.Sprintf("v%s", version.Name), decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(fmt.Sprintf("(%s)", strconv.Itoa(int(version.Code))), decor.WC{C: decor.DSyncSpaceR}),
			decor.Name(source.Name(), decor.WC{C: decor.DSyncSpaceR}),
		),
		mpb.AppendDecorators(
			decor.Percentage(decor.WC{W: 5}),
		),
	)

	mu.Lock()
	if _, exists := sourceLocks[source.Name()]; !exists {
		sourceLocks[source.Name()] = &sync.Mutex{}
		sourceCounts[source.Name()] = 0
	}
	sourceLock := sourceLocks[source.Name()]
	mu.Unlock()

	for {
		sourceLock.Lock()
		if sourceCounts[source.Name()] < source.MaxParallelsDownloads() {
			sourceCounts[source.Name()]++
			sourceLock.Unlock()
			break
		}
		sourceLock.Unlock()
		// Wait before retrying
		time.Sleep(100 * time.Millisecond)
	}
	barWait.IncrBy(1)

	defer func() {
		sourceLock.Lock()
		sourceCounts[source.Name()]--
		sourceLock.Unlock()
	}()

	var outFile string
	if outputFileName != "" {
		outFile = outputFileName
	} else {
		outFile = fmt.Sprintf("%s-%s-v%d.apk", pkgName, version.Name, version.Code)
		outFile = sanitizeFileName(outFile)
	}
	if outputDir != "" {
		outFile = fmt.Sprintf("%s/%s", outputDir, outFile)
	}
	if _, err := os.Stat(outFile); err == nil {
		if !forceDownload {
			collectedErrors = append(collectedErrors, fmt.Sprintf("File %s already exists. Use --force to overwrite.", outFile))
			showErrorBar(progress, bar, pkgName, "error")
			return collectedErrors
		}
		if err := os.Remove(outFile); err != nil {
			collectedErrors = append(collectedErrors, fmt.Sprintf("Error removing existing file %s: %v", outFile, err))
			showErrorBar(progress, bar, pkgName, "error")
			return collectedErrors
		}
	}
	reader, err := source.Download(version)
	if err != nil {
		collectedErrors = append(collectedErrors, fmt.Sprintf("Error downloading package %s from source %s: %v", pkgName, source.Name(), err))
		showErrorBar(progress, bar, pkgName, "error")
		return collectedErrors
	}
	progressReader := bar.ProxyReader(reader)
	defer progressReader.Close()
	file, err := os.Create(outFile)
	if err != nil {
		collectedErrors = append(collectedErrors, fmt.Sprintf("Error creating file %s: %v", outFile, err))
		showErrorBar(progress, bar, pkgName, "error")
		return collectedErrors
	}
	defer file.Close()

	if _, err = io.Copy(file, progressReader); err != nil {
		collectedErrors = append(collectedErrors, fmt.Sprintf("Error saving file %s: %v", outFile, err))
		showErrorBar(progress, bar, pkgName, "error")
		return collectedErrors
	}

	return collectedErrors
}

func removeElements(source, toRemove []string) []string {
	// Создаем map для быстрого поиска элементов, которые нужно удалить
	removeMap := make(map[string]struct{})
	for _, item := range toRemove {
		removeMap[item] = struct{}{}
	}

	// Отфильтровываем элементы
	result := make([]string, 0, len(source))
	for _, item := range source {
		if _, found := removeMap[item]; !found {
			result = append(result, item)
		}
	}
	return result
}
