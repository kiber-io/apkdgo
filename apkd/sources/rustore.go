package sources

import (
	"archive/zip"
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/kiber-io/apkd/apkd/devices"
	"github.com/kiber-io/apkd/apkd/network"
)

type RuStore struct {
	BaseSource
	appsCache                 map[string]map[string]any
	appsCacheMu               sync.RWMutex
	device                    devices.Device
	config                    RuStoreConfig
	latestVersionOnce         sync.Once
	latestVersionCheckEnabled bool
}

type RuStoreUpdateBody struct {
	LatestVersionCode string `json:"latestVersion"`
	LatestVersionName string `json:"latestVersionName"`
}

type RuStoreUpdate struct {
	Body RuStoreUpdateBody `json:"body"`
}

type RuStoreConfig struct {
	BaseSourceConfig `yaml:",inline"`
	AppVersion       string `yaml:"app_version"`
	AppVersionCode   string `yaml:"app_version_code"`
	FirmwareLang     string `yaml:"firmware_lang"`
}

var ruStoreVerCodeRegexp = regexp.MustCompile(`^\d+$`)
var firmwareLangRegexp = regexp.MustCompile(`^[a-z]{2,8}$`)

func (s *RuStore) Name() string {
	return "rustore"
}

func defaultRuStoreConfig() RuStoreConfig {
	return RuStoreConfig{
		AppVersion:     "1.103.1.0",
		AppVersionCode: "1103100",
		FirmwareLang:   "ru",
	}
}

func (s *RuStore) ensureLatestVersion() {
	if !s.latestVersionCheckEnabled {
		return
	}
	s.latestVersionOnce.Do(func() {
		rustoreUpdate, err := s.getLatestRustoreVersion()
		if err != nil {
			s.Log().Logw(fmt.Sprintf("Failed to get latest RuStore version: %v, using hardcoded default values. They may be outdated. Please update your profile or report an issue.", err))
			return
		}
		s.Log().Logd(fmt.Sprintf("Latest RuStore version: %s (code: %s)", rustoreUpdate.Body.LatestVersionName, rustoreUpdate.Body.LatestVersionCode))

		headersToUpdate := make(http.Header)
		if _, exists := s.config.Headers[http.CanonicalHeaderKey("ruStoreVerCode")]; !exists {
			headersToUpdate.Set("ruStoreVerCode", rustoreUpdate.Body.LatestVersionCode)
		}
		if _, exists := s.config.Headers[http.CanonicalHeaderKey("User-Agent")]; !exists {
			headersToUpdate.Set("User-Agent", buildUserAgent(rustoreUpdate.Body.LatestVersionName, s.device))
		}
		if len(headersToUpdate) == 0 {
			return
		}
		if err := network.UpdateDoerDefaultHeaders(s.Net, headersToUpdate); err != nil {
			s.Log().Logw(fmt.Sprintf("Failed to update default headers with latest RuStore version: %v", err))
		}
	})
}

func (s *RuStore) getLatestRustoreVersion() (RuStoreUpdate, error) {
	url := "https://backapi.rustore.ru/rustore-info/new-version"
	req, err := s.NewRequest("GET", url, nil)
	if err != nil {
		return RuStoreUpdate{}, err
	}

	res, err := s.Http().Do(req)
	if err != nil {
		return RuStoreUpdate{}, fmt.Errorf("failed to fetch latest RuStore version: %w", err)
	}

	defer res.Body.Close()
	body, err := readBody(res)
	if err != nil {
		return RuStoreUpdate{}, err
	}
	if res.StatusCode != http.StatusOK {
		return RuStoreUpdate{}, fmt.Errorf("failed to get latest RuStore version (%d): %s", res.StatusCode, body)
	}

	var rustoreUpdate RuStoreUpdate
	if err := json.Unmarshal(body, &rustoreUpdate); err != nil {
		return RuStoreUpdate{}, fmt.Errorf("failed to parse latest RuStore version response: %w", err)
	}

	if rustoreUpdate.Body.LatestVersionCode == "" {
		return RuStoreUpdate{}, fmt.Errorf("latestVersion not found in latest RuStore version response")
	}

	return rustoreUpdate, nil
}

func (s *RuStore) Download(version Version) (*DownloadStream, error) {
	appInfo, err := s.getAppInfo(version.PackageName)
	if err != nil {
		return nil, err
	}
	appId, ok := appInfo["appId"].(float64)
	if !ok {
		return nil, errors.New("appId not found or invalid in app info")
	}
	downloadLink, err := s.getDownloadLink(appId)
	if err != nil {
		return nil, err
	}
	req, err := s.NewRequest("GET", downloadLink, nil)
	if err != nil {
		return nil, err
	}
	return createResponseReader(s.Http(), req)
}

func (s *RuStore) generateDeviceId() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b1 := make([]byte, 16)
	_, err := crand.Read(b1)
	if err != nil {
		// Fallback to math/rand
		for i := range b1 {
			b1[i] = charset[mrand.Intn(len(charset))]
		}
	} else {
		for i := range b1 {
			b1[i] = charset[b1[i]%byte(len(charset))]
		}
	}

	const digits = "0123456789"
	b2 := make([]byte, 10)
	_, err = crand.Read(b2)
	if err != nil {
		// Fallback to math/rand
		for i := range b2 {
			b2[i] = digits[mrand.Intn(len(digits))]
		}
	} else {
		for i := range b2 {
			b2[i] = digits[b2[i]%byte(len(digits))]
		}
	}

	return string(b1) + "-" + string(b2)
}

func (s *RuStore) getAppInfo(packageName string) (map[string]any, error) {
	s.ensureLatestVersion()
	s.appsCacheMu.RLock()
	appInfo, ok := s.appsCache[packageName]
	s.appsCacheMu.RUnlock()
	if ok {
		return appInfo, nil
	}
	// If the app info is not in the cache, fetch it from the API
	// and store it in the cache
	url := "https://backapi.rustore.ru/applicationData/overallInfo/" + packageName
	req, err := s.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	res, err := s.Http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app info: %w", err)
	}

	defer res.Body.Close()
	body, err := readBody(res)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusNotFound {
			return nil, &AppNotFoundError{PackageName: packageName}
		}
		return nil, fmt.Errorf("failed to get app info (%d): %s", res.StatusCode, body)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse app info: %w", err)
	}

	if result["code"] == "OK" {
		appInfo, ok := result["body"].(map[string]any)
		if !ok {
			return nil, errors.New("body not found or invalid in app info response")
		}
		s.appsCacheMu.Lock()
		if s.appsCache == nil {
			s.appsCache = make(map[string]map[string]any)
		}
		if cachedAppInfo, ok := s.appsCache[packageName]; ok {
			s.appsCacheMu.Unlock()
			return cachedAppInfo, nil
		}
		s.appsCache[packageName] = appInfo
		s.appsCacheMu.Unlock()
		return appInfo, nil
	}
	return nil, &AppNotFoundError{PackageName: packageName}
}

func (s *RuStore) getDownloadLink(appId float64) (string, error) {
	s.ensureLatestVersion()
	url := "https://backapi.rustore.ru/applicationData/v2/download-link"
	payloadData := map[string]any{
		"appId":                appId,
		"firstInstall":         true,
		"mobileServices":       []string{"GMS"},
		"supportedAbis":        s.device.CPUAbis,
		"screenDensity":        s.device.DPI,
		"supportedLocales":     []string{"en_US", "ru_RU"},
		"sdkVersion":           s.device.SDKInt,
		"withoutSplits":        true,
		"signatureFingerprint": nil,
	}
	payloadBytes, err := json.Marshal(payloadData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal download link request: %w", err)
	}
	payload := bytes.NewReader(payloadBytes)
	req, err := s.NewRequest("POST", url, payload)
	if err != nil {
		return "", err
	}

	resp, err := s.Http().Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch download link: %w", err)
	}

	defer resp.Body.Close()
	body, err := readBody(resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return "", &AppNotFoundError{PackageName: strconv.Itoa(int(appId))}
		}
		return "", fmt.Errorf("failed to get download link (%d): %s", resp.StatusCode, body)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse download link response: %w", err)
	}
	if _, ok := result["error"]; ok {
		errMsg, ok := result["error"].(string)
		if !ok {
			return "", errors.New("error field is not a string in download link response")
		}
		return "", errors.New(errMsg)
	}
	if result["code"] != "OK" {
		msg, ok := result["message"].(string)
		if !ok {
			return "", errors.New("message field is not a string in download link response")
		}
		return "", errors.New(msg)
	}
	bodyMap, ok := result["body"].(map[string]any)
	if !ok {
		return "", errors.New("body not found or invalid in download link response")
	}
	downloadUrls, ok := bodyMap["downloadUrls"].([]any)
	if !ok || len(downloadUrls) == 0 {
		return "", errors.New("downloadUrls not found or empty in download link response")
	}
	downloadUrlEntry, ok := downloadUrls[0].(map[string]any)
	if !ok {
		return "", errors.New("first downloadUrl entry is not a map in download link response")
	}
	urlStr, ok := downloadUrlEntry["url"].(string)
	if !ok {
		return "", errors.New("url field not found or invalid in download link response")
	}
	return urlStr, nil
}

func (s *RuStore) FindByPackage(packageName string, versionCode int) (Version, error) {
	appInfo, err := s.getAppInfo(packageName)
	if err != nil {
		return Version{}, err
	}
	size, ok := appInfo["fileSize"].(float64)
	if !ok {
		return Version{}, errors.New("fileSize not found or invalid in app info")
	}
	versionName, ok := appInfo["versionName"].(string)
	if !ok {
		return Version{}, errors.New("versionName not found or invalid in app info")
	}
	versionCodeApi, ok := appInfo["versionCode"].(float64)
	if !ok {
		return Version{}, errors.New("versionCode not found or invalid in app info")
	}
	if versionCode != 0 && versionCode != int(versionCodeApi) {
		return Version{}, &AppNotFoundError{PackageName: packageName}
	}
	developerId, ok := appInfo["publicCompanyId"].(string)
	if !ok {
		return Version{}, errors.New("publicCompanyId not found or invalid in app info")
	}
	version := Version{
		Name:        versionName,
		Code:        int(versionCodeApi),
		Size:        uint64(size),
		PackageName: packageName,
		DeveloperId: developerId,
		Type:        APK,
	}
	return version, nil
}

func (s *RuStore) MaxParallelsDownloads() int {
	return 3
}

func (s *RuStore) FindByDeveloper(developerId string) ([]string, error) {
	s.ensureLatestVersion()
	url := "https://backapi.rustore.ru/applicationData/devs/" + developerId + "/apps?limit=1000"
	req, err := s.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	res, err := s.Http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch developer apps: %w", err)
	}

	defer res.Body.Close()
	body, err := readBody(res)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusNotFound {
			return nil, &AppNotFoundError{PackageName: developerId}
		}
		return nil, fmt.Errorf("failed to get developer apps (%d): %s", res.StatusCode, body)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse developer apps response: %w", err)
	}
	if result["code"] != "OK" {
		msg, ok := result["message"].(string)
		if !ok {
			return nil, errors.New("message field is not a string in developer apps response")
		}
		return nil, errors.New(msg)
	}
	devBodyMap, ok := result["body"].(map[string]any)
	if !ok {
		return nil, errors.New("body not found or invalid in developer apps response")
	}
	elements, ok := devBodyMap["elements"].([]any)
	if !ok {
		return nil, errors.New("elements not found or invalid in developer apps response")
	}
	var packages []string
	for _, app := range elements {
		appInfo, ok := app.(map[string]any)
		if !ok {
			return nil, errors.New("app info is not a map in developer apps response")
		}
		packageName, exist := appInfo["packageName"]
		if !exist {
			return nil, errors.New("packageName not found in app info")
		}
		pkgName, ok := packageName.(string)
		if !ok {
			return nil, errors.New("packageName is not a string in app info")
		}
		packages = append(packages, pkgName)
	}
	return packages, nil
}

func replaceFileSafely(srcFile, dstFile string) error {
	if srcFile == dstFile {
		return nil
	}

	if err := os.Rename(srcFile, dstFile); err == nil {
		return nil
	}

	backupFile := dstFile + ".bak"
	backupCreated := false
	if err := os.Rename(dstFile, backupFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to backup destination file %s: %w", dstFile, err)
	} else if err == nil {
		backupCreated = true
	}

	if err := os.Rename(srcFile, dstFile); err != nil {
		if backupCreated {
			if restoreErr := os.Rename(backupFile, dstFile); restoreErr != nil {
				return fmt.Errorf("failed to replace %s with %s (also failed to restore backup: %w): %w", dstFile, srcFile, restoreErr, err)
			}
		}
		return fmt.Errorf("failed to replace %s with %s: %w", dstFile, srcFile, err)
	}

	if backupCreated {
		_ = os.Remove(backupFile)
	}
	return nil
}

func (s *RuStore) ExtractApkFromZip(zipFile, outFile string) (retErr error) {
	r, err := zip.OpenReader(zipFile)
	if err != nil {
		return fmt.Errorf("failed to open zip file %s: %w", zipFile, err)
	}
	closeZipReader := func() error {
		if r == nil {
			return nil
		}
		if closeErr := r.Close(); closeErr != nil {
			return fmt.Errorf("failed to close zip reader for %s: %w", zipFile, closeErr)
		}
		r = nil
		return nil
	}
	defer func() {
		if closeErr := closeZipReader(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()

	if len(r.File) == 0 {
		return fmt.Errorf("zip archive %s is empty", zipFile)
	}

	hasManifest := false
	var apkFile *zip.File
	for _, f := range r.File {
		s.Log().Logd("Checking file in zip: " + f.Name)
		if f.FileInfo().IsDir() {
			continue
		}
		baseName := filepath.Base(f.Name)
		if strings.EqualFold(baseName, "AndroidManifest.xml") {
			hasManifest = true
			break
		}
		if apkFile == nil && strings.EqualFold(filepath.Ext(baseName), ".apk") {
			apkFile = f
		}
	}

	if hasManifest {
		// The zip file is already an APK, no need to extract
		s.Log().Logd(fmt.Sprintf("The file %s is already an APK, skipping extraction", zipFile))
		if err := closeZipReader(); err != nil {
			return err
		}
		if err := replaceFileSafely(zipFile, outFile); err != nil {
			return err
		}
		return nil
	}

	if apkFile == nil {
		return fmt.Errorf("no .apk file found in archive %s", zipFile)
	}

	s.Log().Logd("Extracting .apk from zip file: " + zipFile)
	tmpFile, err := os.CreateTemp(filepath.Dir(outFile), filepath.Base(outFile)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpPath := tmpFile.Name()
	closeTmpFile := func() error {
		if tmpFile == nil {
			return nil
		}
		if closeErr := tmpFile.Close(); closeErr != nil {
			return fmt.Errorf("failed to close temporary file %s: %w", tmpPath, closeErr)
		}
		tmpFile = nil
		return nil
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	var rc io.ReadCloser
	closeArchiveReader := func() error {
		if rc == nil {
			return nil
		}
		if closeErr := rc.Close(); closeErr != nil {
			return fmt.Errorf("failed to close archive file %s: %w", apkFile.Name, closeErr)
		}
		rc = nil
		return nil
	}
	rc, err = apkFile.Open()
	if err != nil {
		wrappedErr := fmt.Errorf("failed to open APK file in archive: %w", err)
		if closeErr := closeTmpFile(); closeErr != nil {
			return errors.Join(wrappedErr, closeErr)
		}
		return wrappedErr
	}
	defer func() {
		if closeErr := closeArchiveReader(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()

	const maxExtractedAPKSize = 8 * 1024 * 1024 * 1024 // 8 GiB
	expectedSize := apkFile.UncompressedSize64
	if expectedSize > maxExtractedAPKSize {
		return fmt.Errorf(
			"embedded APK %s is too large: %d bytes exceeds limit %d",
			apkFile.Name, expectedSize, maxExtractedAPKSize,
		)
	}

	limitedReader := io.LimitReader(rc, int64(maxExtractedAPKSize)+1)
	written, err := io.Copy(tmpFile, limitedReader)

	if err != nil {
		wrappedErr := fmt.Errorf("failed to extract APK: %w", err)
		if closeErr := closeTmpFile(); closeErr != nil {
			return errors.Join(wrappedErr, closeErr)
		}
		return wrappedErr
	}
	if written > int64(maxExtractedAPKSize) {
		return fmt.Errorf(
			"embedded APK %s exceeds extraction limit %d bytes",
			apkFile.Name, maxExtractedAPKSize,
		)
	}
	if expectedSize > 0 && uint64(written) != expectedSize {
		s.Log().Logw(fmt.Sprintf(
			"Extracted APK size mismatch for %s: wrote %d bytes, expected %d bytes",
			apkFile.Name, written, expectedSize,
		))
	}
	if err := closeArchiveReader(); err != nil {
		return err
	}
	if err := closeTmpFile(); err != nil {
		return err
	}
	if err := closeZipReader(); err != nil {
		return err
	}

	if err := replaceFileSafely(tmpPath, outFile); err != nil {
		return err
	}
	cleanupTmp = false

	if zipFile != outFile {
		if err := os.Remove(zipFile); err != nil {
			return fmt.Errorf("failed to remove zip file %s: %w", zipFile, err)
		}
	}
	return nil
}

func buildUserAgent(appVersion string, device devices.Device) string {
	return fmt.Sprintf(
		"RuStore/%s (Android %s; SDK %d; %s; %s %s; ru)",
		appVersion,
		device.AndroidVersion,
		device.SDKInt,
		device.CPUAbis[0],
		device.Manufacturer,
		device.Model,
	)
}

func newRuStoreSource() (Source, error) {
	s := &RuStore{
		appsCache: make(map[string]map[string]any),
		device:    devices.RandomDevice(),
	}
	defaultConfig := defaultRuStoreConfig()
	config, err := ResolveSourceConfig(s.Name(), defaultConfig)
	if err != nil {
		return nil, err
	}
	s.Source = s
	s.config = config
	s.Log().Logd(fmt.Sprintf("Initialized with device: %s %s (Android %s, SDK %d)", s.device.Brand, s.device.Model, s.device.AndroidVersion, s.device.SDKInt))
	s.Log().Logd(fmt.Sprintf("Using config: %+v", config))
	s.latestVersionCheckEnabled = defaultConfig.AppVersion == config.AppVersion && defaultConfig.AppVersionCode == config.AppVersionCode
	headers := ApplyConfiguredHeaders(http.Header{
		"User-Agent":             {buildUserAgent(s.config.AppVersion, s.device)},
		"deviceId":               {s.generateDeviceId()},
		"deviceManufacturerName": {s.device.Manufacturer},
		"deviceModelName":        {s.device.Model},
		"deviceModel":            {s.device.Manufacturer + " " + s.device.Model},
		"firmwareLang":           {config.FirmwareLang},
		"androidSdkVer":          {strconv.Itoa(s.device.SDKInt)},
		"firmwareVer":            {s.device.AndroidVersion},
		"deviceType":             {"mobile"},
		"ruStoreVerCode":         {s.config.AppVersionCode},
		"Content-Type":           {"application/json; charset=utf-8"},
	}, config.Headers)
	s.Net = network.DefaultClientForSource(s.Name()).WithDefaultHeaders(headers)

	return s, nil
}

func init() {
	RegisterSourceFactoryWithConfig(
		newRuStoreSource,
		"rustore",
		NewConfigDecoderWithDefaults(
			defaultRuStoreConfig(),
			func(c *RuStoreConfig) {
				NormalizeBaseSourceConfig(&c.BaseSourceConfig)
				c.FirmwareLang = strings.ToLower(strings.TrimSpace(c.FirmwareLang))
			},
			func(c RuStoreConfig) error {
				if err := ValidateBaseSourceConfig(c.BaseSourceConfig); err != nil {
					return err
				}
				if !appVersionRegexp.MatchString(c.AppVersion) {
					return fmt.Errorf("app_version %q is invalid", c.AppVersion)
				}
				if !ruStoreVerCodeRegexp.MatchString(c.AppVersionCode) {
					return fmt.Errorf("app_version_code %q must contain only digits", c.AppVersionCode)
				}
				if !firmwareLangRegexp.MatchString(c.FirmwareLang) {
					return fmt.Errorf("firmware_lang %q must match [a-z]{2,8}", c.FirmwareLang)
				}
				return nil
			},
		),
	)
}
