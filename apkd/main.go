package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"

	_ "kiber-io/apkd/apkd/devices"
	"kiber-io/apkd/apkd/sources"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb"
	"github.com/vbauerster/mpb/decor"
)

var packageNames []string
var packagesFile string
var selectedSources []string
var forceDownload bool

var rootCmd = cobra.Command{
	Use:   "apkd",
	Short: "apkd is a tool for downloading APKs from multiple sources",
	PreRun: func(cmd *cobra.Command, args []string) {
		for i, src := range selectedSources {
			selectedSources[i] = strings.ToLower(src)
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
				packageNames = append(packageNames, packageName)
			}
		}
		if len(packageNames) > 0 {
			downloadPackage(packageNames)
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

func existsInArray(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func getLatestVersion(packageName string) (sources.Version, sources.Source, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	sourcesMap := sources.GetAll()
	if len(selectedSources) > 0 {
		for src := range sourcesMap {
			if !existsInArray(selectedSources, src) {
				delete(sourcesMap, src)
			}
		}
	}
	var latestSource sources.Source
	var latestVersion sources.Version
	if len(sourcesMap) == 0 {
		return latestVersion, latestSource, errors.New("no sources found for downloading the package " + packageName)
	}
	for _, source := range sourcesMap {
		wg.Add(1)
		go func(src sources.Source) {
			defer wg.Done()
			version, err := src.FindLatestVersion(packageName)
			if err != nil {
				fmt.Printf("Error getting latest version from source %s: %v\n", src.Name(), err)
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

	if latestVersion == (sources.Version{}) || latestSource == nil {
		return latestVersion, latestSource, errors.New("No version found for the package " + packageName)
	}
	return latestVersion, latestSource, nil
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

func downloadPackage(packageNames []string) {
	var wg sync.WaitGroup
	progress := mpb.New(mpb.WithWaitGroup(&wg))

	for _, packageName := range packageNames {
		wg.Add(1)
		go func(pkgName string) {
			defer wg.Done()

			version, source, err := getLatestVersion(pkgName)
			if err != nil {
				fmt.Printf("Error getting latest version for package %s from source %s: %v\n", pkgName, source.Name(), err)
				return
			}

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
			}
			bar := progress.AddBar(version.Size,
				mpb.PrependDecorators(
					decor.Name(fmt.Sprintf("%s | %s: ", pkgName, source.Name())),
				),
				mpb.AppendDecorators(
					decor.CountersKibiByte("% .2f / % .2f"),
				),
			)
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
	progress.Wait()
}
