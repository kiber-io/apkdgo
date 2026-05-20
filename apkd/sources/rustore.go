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
	appsCache   map[string]map[string]any
	appsCacheMu sync.RWMutex
	device      devices.Device
}

type RuStoreProfile struct {
	AppVersion     string `yaml:"app_version"`
	AppVersionCode string `yaml:"app_version_code"`
	FirmwareLang   string `yaml:"firmware_lang"`
}

var ruStoreVerCodeRegexp = regexp.MustCompile(`^\d+$`)
var firmwareLangRegexp = regexp.MustCompile(`^[a-z]{2,8}$`)

func (s *RuStore) Name() string {
	return "rustore"
}

func defaultRuStoreProfile() RuStoreProfile {
	return RuStoreProfile{
		AppVersion:     "1.93.0.3",
		AppVersionCode: "1093003",
		FirmwareLang:   "ru",
	}
}

func (s *RuStore) Download(version Version) (io.ReadCloser, error) {
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

	const maxAPKSize = 2 * 1024 * 1024 * 1024 // 2 GiB
	if _, err := io.Copy(tmpFile, io.LimitReader(rc, maxAPKSize)); err != nil {
		wrappedErr := fmt.Errorf("failed to extract APK: %w", err)
		if closeErr := closeTmpFile(); closeErr != nil {
			return errors.Join(wrappedErr, closeErr)
		}
		return wrappedErr
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

func newRuStoreSource() (Source, error) {
	s := &RuStore{
		appsCache: make(map[string]map[string]any),
		device:    devices.RandomDevice(),
	}
	profile, err := ResolveSourceProfile(s.Name(), defaultRuStoreProfile())
	if err != nil {
		return nil, err
	}
	s.Source = s
	s.Log().Logd(fmt.Sprintf("Initialized with device: %s %s (Android %s, SDK %d)", s.device.Brand, s.device.Model, s.device.AndroidVersion, s.device.SDKInt))
	s.Log().Logd(fmt.Sprintf("Using profile: %+v", profile))
	headers := network.ApplySourceHeaderOverrides(s.Name(), http.Header{
		"User-Agent":             {fmt.Sprintf("RuStore/%s (Android %s; SDK %d; %s; %s %s; ru)", profile.AppVersion, s.device.AndroidVersion, s.device.SDKInt, s.device.CPUAbis[0], s.device.Manufacturer, s.device.Model)},
		"deviceId":               {s.generateDeviceId()},
		"deviceManufacturerName": {s.device.Manufacturer},
		"deviceModelName":        {s.device.Model},
		"deviceModel":            {s.device.Manufacturer + " " + s.device.Model},
		"firmwareLang":           {profile.FirmwareLang},
		"androidSdkVer":          {strconv.Itoa(s.device.SDKInt)},
		"firmwareVer":            {s.device.AndroidVersion},
		"deviceType":             {"mobile"},
		"ruStoreVerCode":         {profile.AppVersionCode},
		"Content-Type":           {"application/json; charset=utf-8"},
	})
	s.Net = network.DefaultClientForSource(s.Name()).WithDefaultHeaders(headers)
	return s, nil
}

func init() {
	RegisterSourceFactoryWithProfile(
		newRuStoreSource,
		"rustore",
		NewProfileDecoderWithDefaults(
			defaultRuStoreProfile(),
			func(p *RuStoreProfile) {
				p.FirmwareLang = strings.ToLower(strings.TrimSpace(p.FirmwareLang))
			},
			func(p RuStoreProfile) error {
				if !appVersionRegexp.MatchString(p.AppVersion) {
					return fmt.Errorf("app_version %q is invalid", p.AppVersion)
				}
				if !ruStoreVerCodeRegexp.MatchString(p.AppVersionCode) {
					return fmt.Errorf("app_version_code %q must contain only digits", p.AppVersionCode)
				}
				if !firmwareLangRegexp.MatchString(p.FirmwareLang) {
					return fmt.Errorf("firmware_lang %q must match [a-z]{2,8}", p.FirmwareLang)
				}
				return nil
			},
		),
	)
}
