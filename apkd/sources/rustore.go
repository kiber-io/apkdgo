package sources

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"io"
	mrand "math/rand"
	"net/http"
	"strconv"

	"github.com/kiber-io/apkd/apkd/devices"
)

type RuStore struct {
	BaseSource
	appsCache map[string]map[string]any
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
	s.addHeaders(req)
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

func (s *RuStore) addHeaders(req *http.Request) {
	device := devices.GetRandomDevice()
	req.Header.Add("User-Agent", "RuStore/1.61.0.2 (Android "+device.BuildVersionRelease+"; SDK "+strconv.Itoa(device.BuildVersionSdkInt)+"; "+device.Platforms[0]+"; "+device.BuildModel+"; en)")
	req.Header.Add("Connection", "Keep-Alive")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("deviceId", s.generateDeviceId())
	req.Header.Add("deviceManufacturerName", device.BuildBrand)
	req.Header.Add("deviceModelName", device.BuildModel)
	req.Header.Add("deviceModel", device.BuildBrand+" "+device.BuildModel)
	req.Header.Add("firmwareLang", "en")
	req.Header.Add("androidSdkVer", strconv.Itoa(device.BuildVersionSdkInt))
	req.Header.Add("firmwareVer", device.BuildVersionRelease)
	req.Header.Add("deviceType", "mobile")
	req.Header.Add("ruStoreVerCode", "1061002")
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
	s.addHeaders(req)

	res, err := http.DefaultClient.Do(req)

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
	device := devices.GetRandomDevice()
	payloadData := map[string]any{
		"appId":          appId,
		"firstInstall":   false,
		"mobileServices": []string{"GMS"},
		"supportedAbis": []string{
			"arm64-v8a", "armeabi-v7a", "x86_64", "x86",
		},
		"screenDensity":        480,
		"supportedLocales":     []string{"en_US", "ru_RU"},
		"sdkVersion":           device.BuildVersionSdkInt,
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
	req.Header.Add("User-Agent", "RuStore/1.61.0.2 (Android "+device.BuildVersionRelease+"; SDK "+strconv.Itoa(device.BuildVersionSdkInt)+"; "+device.Platforms[0]+"; "+device.BuildModel+"; en)")
	req.Header.Add("Connection", "Keep-Alive")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("deviceId", s.generateDeviceId())
	req.Header.Add("deviceManufacturerName", device.BuildBrand)
	req.Header.Add("deviceModelName", device.BuildModel)
	req.Header.Add("deviceModel", device.BuildBrand+" "+device.BuildModel)
	req.Header.Add("firmwareLang", "en")
	req.Header.Add("androidSdkVer", strconv.Itoa(device.BuildVersionSdkInt))
	req.Header.Add("firmwareVer", device.BuildVersionRelease)
	req.Header.Add("deviceType", "mobile")
	req.Header.Add("ruStoreVerCode", "1061002")
	req.Header.Add("Content-Type", "application/json; charset=utf-8")

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return "", err
	}

	defer res.Body.Close()
	body, err := readBody(res)
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusNotFound {
			return "", &AppNotFoundError{PackageName: strconv.Itoa(int(appId))}
		}
		return "", errors.New("failed to get download link (" + strconv.Itoa(res.StatusCode) + "): " + string(body))
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
	res, err := http.DefaultClient.Do(req)

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

func init() {
	s := &RuStore{
		appsCache: make(map[string]map[string]any),
	}
	s.Source = s
	Register(s)
}
