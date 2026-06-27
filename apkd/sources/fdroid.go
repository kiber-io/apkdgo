package sources

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/goccy/go-json"
	"github.com/kiber-io/apkd/apkd/network"
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
	jsonCacheMu sync.Mutex
	jsonCache   map[string]any
}

type FDroidConfig struct {
	BaseSourceConfig `yaml:",inline"`
	AppVersion       string `yaml:"appVersion"`
}

func defaultFDroidConfig() FDroidConfig {
	return FDroidConfig{
		AppVersion: "1.23.1",
	}
}

func (s *FDroid) Name() string {
	return "fdroid"
}

func (s *FDroid) Download(version Version) (*DownloadStream, error) {
	req, err := s.NewRequest("GET", "https://f-droid.org/repo"+version.Link, nil)
	if err != nil {
		return nil, err
	}
	return createResponseReader(s.Http(), req)
}

func (s *FDroid) getJson() (map[string]any, error) {
	s.jsonCacheMu.Lock()
	defer s.jsonCacheMu.Unlock()
	if s.jsonCache != nil {
		return s.jsonCache, nil
	}
	url := "https://f-droid.org/repo/index-v2.json"

	req, err := s.NewRequest("GET", url, nil)

	if err != nil {
		return nil, err
	}

	res, err := s.Http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch fdroid index: %w", err)
	}
	defer res.Body.Close()

	reader := res.Body
	if res.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(res.Body)
		if err != nil {
			return nil, fmt.Errorf("error creating gzip reader: %w", err)
		}
		reader = gzipReader
		defer reader.Close()
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error: %s", res.Status)
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
		return nil, errors.New("invalid JSON format, expected 'packages' object")
	}
	s.jsonCache = packages

	return packages, nil
}

func (s *FDroid) getAppInfo(data map[string]any, packageName string) (AppInfo, error) {
	var appInfo AppInfo
	for pkgName := range data {
		if !strings.EqualFold(pkgName, packageName) {
			continue
		}
		jsonBytes, err := json.Marshal(data[pkgName])
		if err != nil {
			return appInfo, fmt.Errorf("error encoding package JSON: %w", err)
		}
		if err := json.Unmarshal(jsonBytes, &appInfo); err != nil {
			return appInfo, fmt.Errorf("error decoding package JSON: %w", err)
		}
		appInfo.PackageName = pkgName
		return appInfo, nil
	}

	return appInfo, &AppNotFoundError{PackageName: packageName}
}

func (s *FDroid) findAllPackagesByAuthor(data map[string]any, authorName string) ([]AppInfo, error) {
	var appsInfo []AppInfo

	for pkgName := range data {
		if metadata, ok := data[pkgName].(map[string]any)["metadata"].(map[string]any); ok {
			if author, ok := metadata["authorName"].(string); ok && author == authorName {
				jsonBytes, err := json.Marshal(data[pkgName])
				if err != nil {
					return nil, fmt.Errorf("error encoding package JSON: %w", err)
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

func (s *FDroid) findNeededVersion(appInfo AppInfo, versionCode int) (Version, error) {
	version := Version{
		Type: APK,
	}
	var err error

	if versionCode != 0 {
		var foundVersion bool
		for _, remoteVersion := range appInfo.Versions {
			if remoteVersion.Manifest.VersionCode != versionCode {
				continue
			}
			version.Name = remoteVersion.Manifest.VersionName
			version.Code = remoteVersion.Manifest.VersionCode
			version.Size = remoteVersion.File.Size
			version.Link = remoteVersion.File.Name
			version.PackageName = appInfo.PackageName
			version.DeveloperId = appInfo.Metadata.AuthorName
			foundVersion = true
			break
		}
		if !foundVersion {
			err = &AppNotFoundError{PackageName: appInfo.PackageName}
		}
	} else {
		var maxVersionCode int
		for _, remoteVersion := range appInfo.Versions {
			if remoteVersion.Manifest.VersionCode > maxVersionCode {
				maxVersionCode = remoteVersion.Manifest.VersionCode
			}
		}
		for _, remoteVersion := range appInfo.Versions {
			if remoteVersion.Manifest.VersionCode != maxVersionCode {
				continue
			}
			version.Name = remoteVersion.Manifest.VersionName
			version.Code = remoteVersion.Manifest.VersionCode
			version.Size = remoteVersion.File.Size
			version.Link = remoteVersion.File.Name
			version.PackageName = appInfo.PackageName
			version.DeveloperId = appInfo.Metadata.AuthorName
			break
		}
	}
	return version, err
}

func (s *FDroid) FindByPackage(packageName string, versionCode int) (Version, error) {
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

func (s *FDroid) FindByDeveloper(developerId string) ([]string, error) {
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

func newFDroidSource() (Source, error) {
	s := &FDroid{}
	s.Source = s
	config, err := ResolveSourceConfig(s.Name(), defaultFDroidConfig())
	if err != nil {
		return nil, err
	}
	s.Log().Logd(fmt.Sprintf("Using config: %+v", config))
	headers := ApplyConfiguredHeaders(http.Header{
		"User-Agent": {"F-Droid " + config.AppVersion},
	}, config.Headers)
	s.Net = network.DefaultClientForSource(s.Name()).WithDefaultHeaders(headers).DisableHTTP2()
	return s, nil
}

func init() {
	RegisterSourceFactoryWithConfig(newFDroidSource, "fdroid",
		NewConfigDecoderWithDefaults(
			defaultFDroidConfig(),
			func(c *FDroidConfig) {
				NormalizeBaseSourceConfig(&c.BaseSourceConfig)
				c.AppVersion = strings.TrimSpace(c.AppVersion)
			},
			func(c FDroidConfig) error {
				if err := ValidateBaseSourceConfig(c.BaseSourceConfig); err != nil {
					return err
				}
				if strings.TrimSpace(c.AppVersion) == "" {
					return errors.New("appVersion cannot be empty")
				}
				if !appVersionRegexp.MatchString(c.AppVersion) {
					return fmt.Errorf("appVersion %q is invalid", c.AppVersion)
				}
				return nil
			},
		),
	)
}
