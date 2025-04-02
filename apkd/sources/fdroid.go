package sources

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/goccy/go-json"
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
	jsonCache map[string]any
}

func (s FDroid) Name() string {
	return "fdroid"
}

func (s FDroid) Download(version Version) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", "https://f-droid.org/repo"+version.Link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("User-Agent", "F-Droid 1.21.1")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("accept-charset", "UTF-8")
	return createResponseReader(req)
}

func (s FDroid) getJson() (map[string]any, error) {
	if s.jsonCache != nil {
		return s.jsonCache, nil
	}
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

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}
	var jsonData map[string]any
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return nil, fmt.Errorf("error unmarshalling JSON: %w", err)
	}
	packages, ok := jsonData["packages"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid JSON format, expected 'packages' object")
	}
	s.jsonCache = packages

	return packages, nil
}

func (s FDroid) getAppInfo(data map[string]any, packageName string) (AppInfo, error) {
	var appInfo AppInfo
	for pkgName := range data {
		if strings.EqualFold(pkgName, packageName) {
			jsonBytes, err := json.Marshal(data[pkgName])
			if err != nil {
				panic(err)
			}
			if err := json.Unmarshal(jsonBytes, &appInfo); err != nil {
				return appInfo, fmt.Errorf("error decoding package JSON: %w", err)
			}
			appInfo.PackageName = pkgName
			return appInfo, nil
		}
	}

	return appInfo, &AppNotFoundError{PackageName: packageName}
}

func (s FDroid) findAllPackagesByAuthor(data map[string]any, authorName string) ([]AppInfo, error) {
	var appsInfo []AppInfo

	for pkgName := range data {
		if metadata, ok := data[pkgName].(map[string]any)["metadata"].(map[string]any); ok {
			if author, ok := metadata["authorName"].(string); ok && author == authorName {
				jsonBytes, err := json.Marshal(data[pkgName])
				if err != nil {
					panic(err)
				}
				var appInfo AppInfo
				if err := json.Unmarshal(jsonBytes, &appInfo); err != nil {
					return nil, fmt.Errorf("error decoding package JSON: %w", err)
				}
				appInfo.PackageName = pkgName
				appsInfo = append(appsInfo, appInfo)
			}
		}
	}

	return appsInfo, nil
}

func (s FDroid) findNeededVersion(appInfo AppInfo, versionCode int) (Version, error) {
	version := Version{
		Type: APK,
	}
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

	data, err := s.getJson()
	if err != nil {
		return version, err
	}
	appInfo, err := s.getAppInfo(data, packageName)
	if err != nil {
		return version, err
	}
	return s.findNeededVersion(appInfo, versionCode)
}

func (s FDroid) FindByDeveloper(developerId string) ([]string, error) {
	var packages []string
	var err error
	data, err := s.getJson()
	if err != nil {
		return packages, err
	}
	appsInfo, err := s.findAllPackagesByAuthor(data, developerId)
	if err != nil {
		return packages, err
	}
	for _, appInfo := range appsInfo {
		packages = append(packages, appInfo.PackageName)
	}
	return packages, nil
}

func init() {
	s := FDroid{}
	s.appsCache = make(map[string]map[string]any)
	Register(s)
}
