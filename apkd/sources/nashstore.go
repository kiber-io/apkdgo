package sources

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/kiber-io/apkd/apkd/devices"
	"github.com/kiber-io/apkd/apkd/network"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type ReleaseNashStore struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	Link        string `json:"install_path"`
}

type AppNashStore struct {
	PackageName string           `json:"app_id"`
	Id          string           `json:"id"`
	Release     ReleaseNashStore `json:"release"`
	Size        uint64           `json:"-"`
}

type AppInfoNashStore struct {
	App AppNashStore `json:"app"`
}

type NashStore struct {
	BaseSource

	device devices.Device
}

type NashStoreConfig struct {
	BaseSourceConfig `yaml:",inline"`
	AppVersion       string `yaml:"appVersion"`
	Token            string `yaml:"token"`
}

func defaultNashStoreConfig() NashStoreConfig {
	return NashStoreConfig{
		AppVersion: "0.0.6",
	}
}

func (s *NashStore) Name() string {
	return "nashstore"
}

func (s *NashStore) answer42() string {
	encrypted := []byte{
		0x31, 0x66, 0x67, 0x66, 0x30, 0x6d, 0x67, 0x37,
		0x67, 0x34, 0x66, 0x36, 0x34, 0x31, 0x30, 0x6c,
		0x60, 0x64, 0x61, 0x66, 0x33, 0x64, 0x36, 0x64,
		0x60, 0x31, 0x6d, 0x33, 0x64, 0x31, 0x6d, 0x61,
		0x65, 0x34, 0x63, 0x36, 0x63, 0x67, 0x30, 0x31,
		0x31, 0x33, 0x65, 0x65, 0x65, 0x6c, 0x64, 0x66,
		0x6d, 0x65, 0x62, 0x6d, 0x31, 0x62, 0x64, 0x65,
		0x6d, 0x37, 0x61, 0x34, 0x34, 0x61, 0x65, 0x34,
	}

	key := byte(0x55)
	for i := range encrypted {
		encrypted[i] ^= key
	}
	return string(encrypted)
}

func (s *NashStore) getRandomTimestamp() int64 {
	generator := rand.New(rand.NewSource(time.Now().UnixNano()))
	minutesToSubtract := generator.Intn(31) + 30
	now := time.Now()
	randomTime := now.Add(-time.Duration(minutesToSubtract) * time.Minute)
	timestampMillis := randomTime.UnixNano() / int64(time.Millisecond)
	return timestampMillis
}

func (s *NashStore) getAppInfo(packageName string) (AppInfoNashStore, error) {
	var appInfo AppInfoNashStore
	url := "https://store.nashstore.ru/api/mobile/v1/profile/updates"
	payloadData := map[string]any{
		"apps": map[string]any{
			packageName: map[string]any{
				"appName":          packageName,
				"versionName":      "1.0",
				"firstInstallTime": s.getRandomTimestamp(),
				"lastUpdateTime":   s.getRandomTimestamp(),
				"versionCode":      1,
				"packageName":      packageName,
			},
		},
	}
	payloadBytes, err := json.Marshal(payloadData)
	if err != nil {
		return appInfo, fmt.Errorf("failed to marshal app info request: %w", err)
	}
	payload := bytes.NewReader(payloadBytes)
	req, err := s.NewRequest("POST", url, payload)

	if err != nil {
		return appInfo, err
	}

	res, err := s.Net.Do(req)
	if err != nil {
		return appInfo, fmt.Errorf("failed to fetch app info: %w", err)
	}

	defer res.Body.Close()
	body, err := readBody(res)
	if err != nil {
		return appInfo, err
	}
	if res.StatusCode != http.StatusOK {
		return appInfo, fmt.Errorf("failed to get app info (%s): %s", res.Status, body)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return appInfo, fmt.Errorf("failed to parse app info response: %w", err)
	}
	if list, ok := result["list"].([]any); ok {
		if len(list) > 1 {
			return appInfo, errors.New("multiple apps found")
		} else if len(list) == 0 {
			return appInfo, &AppNotFoundError{PackageName: packageName}
		}
		if len(list) == 1 {
			jsonAppInfo, err := json.Marshal(list[0])
			if err != nil {
				return appInfo, fmt.Errorf("failed to marshal app info entry: %w", err)
			}
			if err := json.Unmarshal(jsonAppInfo, &appInfo.App); err != nil {
				return appInfo, fmt.Errorf("failed to unmarshal app info entry: %w", err)
			}
			appInfoMap, ok := list[0].(map[string]any)
			if !ok {
				return appInfo, fmt.Errorf("failed to parse app info: unexpected list element type %T", list[0])
			}
			sizeRaw, exists := appInfoMap["size"]
			if !exists {
				return appInfo, errors.New("failed to parse app info: field size is missing")
			}
			size, ok := sizeRaw.(float64)
			if !ok {
				return appInfo, fmt.Errorf("failed to parse app info: field size has unexpected type %T", sizeRaw)
			}
			if size < 0 {
				return appInfo, errors.New("failed to parse app info: field size must be >= 0")
			}
			appInfo.App.Size = uint64(size)
			return appInfo, nil
		}
	}
	return appInfo, errors.New("failed to parse app info")
}

func (s *NashStore) FindByPackage(packageName string, versionCode int) (Version, error) {
	var version Version
	appInfo, err := s.getAppInfo(packageName)
	if err != nil {
		return version, err
	}
	if versionCode != 0 && versionCode != appInfo.App.Release.VersionCode {
		return Version{}, &AppNotFoundError{PackageName: packageName}
	}
	version.Name = appInfo.App.Release.VersionName
	version.Code = appInfo.App.Release.VersionCode
	version.Size = appInfo.App.Size
	version.PackageName = appInfo.App.PackageName
	version.DeveloperId = appInfo.App.Id
	version.Link = appInfo.App.Release.Link
	version.Type = APK

	return version, nil
}

func (s *NashStore) Download(version Version) (*DownloadStream, error) {
	req, err := s.NewRequest("GET", version.Link, nil)
	if err != nil {
		return nil, err
	}
	return createResponseReader(s.Http(), req)
}

func (s *NashStore) MaxParallelsDownloads() int {
	return 3
}

func (s *NashStore) FindByDeveloper(developerId string) ([]string, error) {
	url := "https://store.nashstore.ru/api/mobile/v1/application/" + developerId

	req, err := s.NewRequest("GET", url, nil)

	if err != nil {
		return nil, err
	}

	res, err := s.Net.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch developer apps: %w", err)
	}

	defer res.Body.Close()
	body, err := readBody(res)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get app info (%s): %s", res.Status, body)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse developer apps response: %w", err)
	}
	appRaw, exists := result["app"]
	if !exists {
		return nil, errors.New("app not found")
	}
	app, ok := appRaw.(map[string]any)
	if !ok || app == nil {
		return nil, fmt.Errorf("failed to parse app info: field app has unexpected type %T", appRaw)
	}

	var packages []string
	otherAppsRaw, exists := app["other_apps"]
	if !exists || otherAppsRaw == nil {
		return packages, nil
	}
	otherApps, ok := otherAppsRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed to parse app info: field other_apps has unexpected type %T", otherAppsRaw)
	}
	appsRaw, exists := otherApps["apps"]
	if !exists || appsRaw == nil {
		return packages, nil
	}
	apps, ok := appsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("failed to parse app info: field other_apps.apps has unexpected type %T", appsRaw)
	}
	if len(apps) <= 1 {
		return packages, nil
	}

	// Cut first app because it is the same as the requested app and we need only other apps.
	for _, appEntry := range apps[1:] {
		appInfoMap, ok := appEntry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("failed to parse app info: unexpected app entry type %T", appEntry)
		}
		packageRaw, exists := appInfoMap["app_id"]
		if !exists {
			return nil, errors.New("failed to parse app info: field app_id is missing")
		}
		packageName, ok := packageRaw.(string)
		if !ok {
			return nil, fmt.Errorf("failed to parse app info: field app_id has unexpected type %T", packageRaw)
		}
		packages = append(packages, packageName)
	}

	return packages, nil
}

func newNashStoreSource() (Source, error) {
	s := &NashStore{
		device: devices.RandomDevice(),
	}
	s.Source = s
	tok := s.answer42()
	appHeader := map[string]any{
		"androidId":   s.device.AndroidID,
		"apiLevel":    s.device.SDKInt,
		"baseOs":      "",
		"buildId":     s.device.BuildID,
		"carrier":     "MTS",
		"deviceName":  s.device.Model,
		"fingerprint": s.device.Fingerprint,
		"fontScale":   1,
		"brand":       s.device.Brand,
		"deviceId":    s.device.Device,
		"width":       s.device.Width,
		"height":      s.device.Height,
		"scale":       2.625,
	}
	appHeaderBytes, err := json.Marshal(appHeader)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal nashstore app header: %w", err)
	}
	config, err := ResolveSourceConfig(s.Name(), defaultNashStoreConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to decode nashstore config: %w", err)
	}
	if config.Token != "" {
		tok = config.Token
	}
	s.Log().Logd(fmt.Sprintf("Using config: %+v", config))
	headers := ApplyConfiguredHeaders(http.Header{
		"User-Agent":    {"Nashstore [com.nashstore][" + config.AppVersion + "][" + cases.Title(language.English).String(s.device.Brand) + "]"},
		"Content-Type":  {"application/json"},
		"xaccesstoken":  {tok},
		"Cookie":        {"nashstore_token=" + tok},
		"nashstore-app": {string(appHeaderBytes)},
	}, config.Headers)
	s.Net = network.DefaultClientForSource(s.Name()).WithDefaultHeaders(headers)
	return s, nil
}

func init() {
	RegisterSourceFactoryWithConfig(newNashStoreSource, "nashstore", NewConfigDecoderWithDefaults(
		defaultNashStoreConfig(),
		func(c *NashStoreConfig) {
			NormalizeBaseSourceConfig(&c.BaseSourceConfig)
			c.AppVersion = strings.TrimSpace(c.AppVersion)
		},
		func(c NashStoreConfig) error {
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
	))
}
