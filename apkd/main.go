package main

import (
	"errors"
	"fmt"
	"io"
	"os"
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

var packageNames []string
var packagesFile string
var selectedSources []string
var forceDownload bool

var activeSources []sources.Source

var rootCmd = cobra.Command{
	Use:   "apkd",
	Short: "apkd is a tool for downloading APKs from multiple sources",
	PreRun: func(cmd *cobra.Command, args []string) {
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
	},
	Run: func(cmd *cobra.Command, args []string) {
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
		if len(packageNames) > 0 {
			downloadPackages(packageNames)
		} else {
			fmt.Println("Please provide a package name using the --package flag")
			os.Exit(1)
		}
	},
}

func main() {
	rootCmd.PersistentFlags().StringArrayVarP(&packageNames, "package", "p", []string{}, "package name of the app")
	rootCmd.PersistentFlags().StringArrayVarP(&selectedSources, "source", "s", []string{}, "Specify source(s) for downloading")
	rootCmd.PersistentFlags().BoolVarP(&forceDownload, "force", "F", false, "Force download even if the file already exists")
	rootCmd.PersistentFlags().StringVarP(&packagesFile, "file", "f", "", "File containing package names")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func getLatestVersion(packageName string) (sources.Version, sources.Source, []sources.Error) {
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
			version, err := src.FindLatestVersion(packageName)
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

func downloadPackages(packageNames []string) {
	var wg sync.WaitGroup
	var sourcesErrors []sources.Error
	progress := mpb.New(mpb.WithAutoRefresh(), mpb.WithWaitGroup(&wg))
	sourceLocks := make(map[string]*sync.Mutex)
	sourceCounts := make(map[string]int)
	mu := sync.Mutex{}

	for _, packageName := range packageNames {
		wg.Add(1)
		go func(pkgName string) {
			defer wg.Done()

			barSearch := progress.AddBar(1,
				mpb.PrependDecorators(
					decor.Name(pkgName, decor.WC{C: decor.DSyncSpaceR}),
					decor.Name(" [searching]", decor.WC{C: decor.DSyncSpaceR}),
				),
			)

			version, source, errs := getLatestVersion(pkgName)
			sourcesErrors = append(sourcesErrors, errs...)
			barSearch.IncrBy(1)
			if version == (sources.Version{}) || source == nil {
				var errorText string
				if len(errs) > 0 {
					errorText = "error"
				} else {
					errorText = "not found"
				}
				barError := progress.AddBar(1,
					mpb.BarQueueAfter(barSearch),
					mpb.PrependDecorators(
						decor.Name(pkgName, decor.WC{C: decor.DSyncSpaceR}),
						decor.Name(" ["+errorText+"]", decor.WC{C: decor.DSyncSpaceR}),
					),
				)
				barError.IncrBy(1)
				return
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

			outFile := fmt.Sprintf("%s-%s-v%d.apk", pkgName, version.Name, version.Code)
			outFile = sanitizeFileName(outFile)
			if _, err := os.Stat(outFile); err == nil {
				if !forceDownload {
					fmt.Printf("File %s already exists. Use --force to overwrite.\n", outFile)
					return
				}
				if err := os.Remove(outFile); err != nil {
					fmt.Printf("Error removing existing file %s: %v\n", outFile, err)
					return
				}
			}
			reader, err := source.Download(version)
			if err != nil {
				fmt.Printf("Error downloading package %s from source %s: %v\n", pkgName, source.Name(), err)
				return
			}
			progressReader := bar.ProxyReader(reader)
			defer progressReader.Close()
			file, err := os.Create(outFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating file: %v\n", err)
				return
			}
			defer file.Close()

			if _, err = io.Copy(file, progressReader); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving file: %v\n", err)
				return
			}
		}(packageName)
	}
	wg.Wait()
	progress.Wait()

	if len(sourcesErrors) > 0 {
		fmt.Println("\nErrors:")
		for _, err := range sourcesErrors {
			fmt.Printf("  Source: %s, Package: %s, Error: %s\n", err.SourceName, err.PackageName, err.Err.Error())
		}
	}
}
