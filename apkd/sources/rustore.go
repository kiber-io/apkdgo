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
	appId := appInfo["appId"].(float64)
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
		return nil, err
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
		return nil, errors.New("failed to get app info (" + strconv.Itoa(res.StatusCode) + "): " + string(body))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if result["code"] == "OK" {
		appInfo := result["body"].(map[string]any)
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
		return "", err
	}
	payload := bytes.NewReader(payloadBytes)
	req, err := s.NewRequest("POST", url, payload)
	if err != nil {
		return "", err
	}

	resp, err := s.Http().Do(req)

	if err != nil {
		return "", err
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
		return "", errors.New("failed to get download link (" + strconv.Itoa(resp.StatusCode) + "): " + string(body))
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if _, ok := result["error"]; ok {
		return "", errors.New(result["error"].(string))
	}
	if result["code"] != "OK" {
		return "", errors.New(result["message"].(string))
	}
	downloadUrl := result["body"].(map[string]any)["downloadUrls"].([]any)[0].(map[string]any)
	return downloadUrl["url"].(string), nil
}

func (s *RuStore) FindByPackage(packageName string, versionCode int) (Version, error) {
	appInfo, err := s.getAppInfo(packageName)
	if err != nil {
		return Version{}, err
	}
	size := appInfo["fileSize"].(float64)
	versionName := appInfo["versionName"].(string)
	versionCodeApi := appInfo["versionCode"].(float64)
	if versionCode != 0 && versionCode != int(versionCodeApi) {
		return Version{}, &AppNotFoundError{PackageName: packageName}
	}
	developerId := appInfo["publicCompanyId"].(string)
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
		return nil, err
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
		return nil, errors.New("failed to get developer apps (" + strconv.Itoa(res.StatusCode) + "): " + string(body))
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result["code"] != "OK" {
		return nil, errors.New(result["message"].(string))
	}
	var packages []string
	for _, app := range result["body"].(map[string]any)["elements"].([]any) {
		appInfo := app.(map[string]any)
		packageName, exist := appInfo["packageName"]
		if !exist {
			return nil, errors.New("packageName not found in app info")
		}
		packages = append(packages, packageName.(string))
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
			_ = os.Rename(backupFile, dstFile)
		}
		return fmt.Errorf("failed to replace %s with %s: %w", dstFile, srcFile, err)
	}

	if backupCreated {
		_ = os.Remove(backupFile)
	}
	return nil
}

func (s *RuStore) ExtractApkFromZip(zipFile string, outFile string) (retErr error) {
	r, err := zip.OpenReader(zipFile)
	if err != nil {
		return err
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
		s.Log().Logd(fmt.Sprintf("Checking file in zip: %s", f.Name))
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

	s.Log().Logd(fmt.Sprintf("Extracting .apk from zip file: %s", zipFile))
	tmpFile, err := os.CreateTemp(filepath.Dir(outFile), filepath.Base(outFile)+".tmp-*")
	if err != nil {
		return err
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
		if closeErr := closeTmpFile(); closeErr != nil {
			return errors.Join(err, closeErr)
		}
		return err
	}
	defer func() {
		if closeErr := closeArchiveReader(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()

	if _, err := io.Copy(tmpFile, rc); err != nil {
		if closeErr := closeTmpFile(); closeErr != nil {
			return errors.Join(err, closeErr)
		}
		return err
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
