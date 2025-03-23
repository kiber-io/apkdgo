package sources

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type AppMetadata struct {
	AuthorName string `json:"authorName"`
}

type VersionFile struct {
	Name string `json:"name"`
	Size uint64 `json:"size"`
}

type VersionManifest struct {
	VersionName string `json:"versionName"`
	VersionCode int    `json:"versionCode"`
}

type VersionJson struct {
	File     VersionFile     `json:"file"`
	Manifest VersionManifest `json:"manifest"`
}

type AppInfo struct {
	PackageName string
	Metadata    AppMetadata            `json:"metadata"`
	Versions    map[string]VersionJson `json:"versions"`
}

type FDroid struct {
	BaseSource
	appsCache map[string]map[string]any
}

func (s FDroid) Name() string {
	return "fdroid"
}

func (s FDroid) Download(version Version) (io.ReadCloser, error) {
	return downloadFile("https://f-droid.org/repo" + version.Link)
}

func (s FDroid) getReader() (io.ReadCloser, error) {
	url := "https://f-droid.org/repo/index-v2.json"

	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		return nil, err
	}

	req.Header.Add("User-Agent", "F-Droid 1.21.1")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("accept-charset", "UTF-8")

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error: %s", res.Status)
	}
	var reader io.ReadCloser = res.Body

	if res.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(res.Body)
		if err != nil {
			return nil, fmt.Errorf("error creating gzip reader: %w", err)
		}
		reader = gzipReader
	}

	return reader, nil
}

func (s FDroid) getAppInfo(reader io.ReadCloser, packageName string) (AppInfo, error) {
	decoder := json.NewDecoder(reader)

	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return AppInfo{}, fmt.Errorf("invalid JSON format, expected '{'")
	}

	var foundPackages bool
	var appInfo AppInfo
	var foundPkg bool

	for decoder.More() {
		if foundPkg {
			break
		}
		token, err := decoder.Token()
		if err != nil {
			return appInfo, fmt.Errorf("error reading key: %w", err)
		}

		key, ok := token.(string)
		if !ok {
			return appInfo, fmt.Errorf("unexpected key format")
		}

		if key == "packages" {
			foundPackages = true
			if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
				return appInfo, fmt.Errorf("invalid JSON format inside 'packages', expected '{'")
			}

			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return appInfo, fmt.Errorf("error reading package name: %w", err)
				}

				pkgName, ok := token.(string)
				if !ok {
					return appInfo, fmt.Errorf("unexpected package name format")
				}

				if strings.EqualFold(pkgName, packageName) {
					if err := decoder.Decode(&appInfo); err != nil {
						return appInfo, fmt.Errorf("error decoding package JSON: %w", err)
					}
					appInfo.PackageName = pkgName
					foundPkg = true
					break
				} else {
					var skip any
					if err := decoder.Decode(&skip); err != nil {
						return appInfo, fmt.Errorf("error skipping package JSON: %w", err)
					}
				}
			}
		} else {
			var skip any
			if err := decoder.Decode(&skip); err != nil {
				return appInfo, fmt.Errorf("error skipping non-packages JSON: %w", err)
			}
		}
	}

	if !foundPackages {
		return appInfo, fmt.Errorf("no 'packages' object found in JSON")
	}
	if foundPkg {
		return appInfo, nil
	}
	return appInfo, &AppNotFoundError{PackageName: packageName}
}

func (s FDroid) findAllPackagesByAuthor(reader io.ReadCloser, authorName string) ([]AppInfo, error) {
	decoder := json.NewDecoder(reader)

	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("invalid JSON format, expected '{'")
	}

	var appsInfo []AppInfo

	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("error reading key: %w", err)
		}

		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected key format")
		}

		if key == "packages" {
			if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
				return nil, fmt.Errorf("invalid JSON format inside 'packages', expected '{'")
			}

			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("error reading key: %w", err)
				}

				packageName, ok := token.(string)
				if !ok {
					return nil, fmt.Errorf("unexpected package name format")
				}

				var appInfo AppInfo
				if err := decoder.Decode(&appInfo); err != nil {
					return nil, fmt.Errorf("error decoding package JSON: %w", err)
				}
				appInfo.PackageName = packageName
				if strings.EqualFold(appInfo.Metadata.AuthorName, authorName) {
					appsInfo = append(appsInfo, appInfo)
				}
			}
		} else {
			var skip any
			if err := decoder.Decode(&skip); err != nil {
				return nil, fmt.Errorf("error skipping non-packages JSON: %w", err)
			}
		}
	}

	return appsInfo, nil
}

func (s FDroid) findNeededVersion(appInfo AppInfo, versionCode int) (Version, error) {
	var version Version
	var err error

	if versionCode != 0 {
		var foundVersion bool
		for _, remoteVersion := range appInfo.Versions {
			if remoteVersion.Manifest.VersionCode == versionCode {
				version.Name = remoteVersion.Manifest.VersionName
				version.Code = remoteVersion.Manifest.VersionCode
				version.Size = remoteVersion.File.Size
				version.Link = remoteVersion.File.Name
				version.PackageName = appInfo.PackageName
				version.DeveloperId = appInfo.Metadata.AuthorName
				foundVersion = true
				break
			}
		}
		if !foundVersion {
			err = &AppNotFoundError{}
		}
	} else {
		var maxVersionCode int
		for _, remoteVersion := range appInfo.Versions {
			if remoteVersion.Manifest.VersionCode > maxVersionCode {
				maxVersionCode = remoteVersion.Manifest.VersionCode
			}
		}
		for _, remoteVersion := range appInfo.Versions {
			if remoteVersion.Manifest.VersionCode == maxVersionCode {
				version.Name = remoteVersion.Manifest.VersionName
				version.Code = remoteVersion.Manifest.VersionCode
				version.Size = remoteVersion.File.Size
				version.Link = remoteVersion.File.Name
				version.PackageName = appInfo.PackageName
				version.DeveloperId = appInfo.Metadata.AuthorName
				break
			}
		}
	}
	return version, err
}

func (s FDroid) FindByPackage(packageName string, versionCode int) (Version, error) {
	var version Version

	reader, err := s.getReader()
	if err != nil {
		return version, err
	}
	defer reader.Close()
	appInfo, err := s.getAppInfo(reader, packageName)
	if err != nil {
		return version, err
	}
	return s.findNeededVersion(appInfo, versionCode)
}

func (s FDroid) MaxParallelsDownloads() int {
	return 3
}

func (s FDroid) FindByDeveloper(developerId string) ([]Version, error) {
	var versions []Version
	var err error
	reader, err := s.getReader()
	if err != nil {
		return versions, err
	}
	defer reader.Close()
	appsInfo, err := s.findAllPackagesByAuthor(reader, developerId)
	if err != nil {
		return versions, err
	}
	for _, appInfo := range appsInfo {
		version, err := s.findNeededVersion(appInfo, 0)
		if err != nil {
			return versions, err
		}
		versions = append(versions, version)
	}
	return versions, nil
}

func init() {
	s := FDroid{}
	s.appsCache = make(map[string]map[string]any)
	Register(s)
}
