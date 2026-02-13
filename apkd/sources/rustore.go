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
	"strconv"
	"strings"

	"github.com/kiber-io/apkd/apkd/devices"
	"github.com/kiber-io/apkd/apkd/logger"
	"github.com/kiber-io/apkd/apkd/network"
)

type RuStore struct {
	BaseSource
	appsCache map[string]map[string]any
	device    devices.Device
}

func (s *RuStore) Name() string {
	return "rustore"
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
	return createResponseReader(req)
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

	return string(b1) + "--" + string(b2)
}

func (s *RuStore) getAppInfo(packageName string) (map[string]any, error) {
	if appInfo, ok := s.appsCache[packageName]; ok {
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
		s.appsCache[packageName] = appInfo
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
		"supportedAbis":        s.device.Platforms,
		"screenDensity":        480,
		"supportedLocales":     []string{"en_US", "ru_RU"},
		"sdkVersion":           s.device.BuildVersionSdkInt,
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

func (s *RuStore) ExtractApkFromZip(zipFile string) error {
	r, err := zip.OpenReader(zipFile)
	if err != nil {
		return err
	}
	defer r.Close()
	hasManifest := false
	for _, f := range r.File {
		logger.Logd(fmt.Sprintf("Checking file in zip: %s", f.Name))
		if strings.EqualFold(f.Name, "AndroidManifest.xml") {
			hasManifest = true
			break
		}
	}
	if hasManifest {
		// The zip file is already an APK, no need to extract
		logger.Logd(fmt.Sprintf("The file %s is already an APK, skipping extraction", zipFile))
		return nil
	}
	logger.Logd(fmt.Sprintf("Extracting .apk from zip file: %s", zipFile))
	parentDir := filepath.Dir(zipFile)
	apkFilePath := filepath.Base(zipFile + ".apk")
	outPath := filepath.Join(parentDir, apkFilePath)

	f := r.File[0]
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	if err != nil {
		return err
	}
	outFile.Close()
	r.Close()
	if err := os.Remove(zipFile); err != nil {
		return fmt.Errorf("failed to remove zip file %s: %v", zipFile, err)
	}
	err = os.Rename(outPath, zipFile)
	if err != nil {
		return fmt.Errorf("failed to rename extracted apk file %s to %s: %v", outPath, zipFile, err)
	}

	return nil
}

func init() {
	s := &RuStore{
		appsCache: make(map[string]map[string]any),
		device:    devices.GetRandomDevice(),
	}
	s.Source = s
	fmt.Printf("Initialized RuStore source with device: %s %s (Android %s, SDK %d)\n", s.device.BuildBrand, s.device.BuildModel, s.device.BuildVersionRelease, s.device.BuildVersionSdkInt)
	s.Net = network.DefaultClient().WithDefaultHeaders(http.Header{
		"User-Agent":             {"RuStore/1.93.0.3 (Android " + s.device.BuildVersionRelease + "; SDK " + strconv.Itoa(s.device.BuildVersionSdkInt) + "; " + s.device.Platforms[0] + "; " + s.device.BuildModel + "; ru)"},
		"deviceId":               {s.generateDeviceId()},
		"deviceManufacturerName": {s.device.BuildBrand},
		"deviceModelName":        {s.device.BuildModel},
		"deviceModel":            {s.device.BuildBrand + " " + s.device.BuildModel},
		"firmwareLang":           {"ru"},
		"androidSdkVer":          {strconv.Itoa(s.device.BuildVersionSdkInt)},
		"firmwareVer":            {s.device.BuildVersionRelease},
		"deviceType":             {"mobile"},
		"ruStoreVerCode":         {"1093003"},
		"Content-Type":           {"application/json; charset=utf-8"},
	})
	Register(s)
}
