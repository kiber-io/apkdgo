package sources

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	mrand "math/rand"
	"net/http"
	"strconv"
)

type RuStore struct {
	appsCache map[string]map[string]any
	devices   []map[string]any
}

func (s RuStore) Name() string {
	return "rustore"
}

func (s RuStore) Download(version Version) (io.ReadCloser, error) {
	appInfo, err := s.getAppInfo(version.PackageName)
	if err != nil {
		return nil, err
	}
	appId := appInfo["appId"].(float64)
	downloadLink, err := s.getDownloadLink(appId)
	if err != nil {
		return nil, err
	}
	return downloadFile(downloadLink)
}

func (s RuStore) generateDeviceId() string {
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

func (s RuStore) getDevice() map[string]any {
	n, err := crand.Int(crand.Reader, big.NewInt(int64(len(s.devices))))
	if err != nil {
		// Fallback to math/rand
		return s.devices[mrand.Intn(len(s.devices))]
	}
	return s.devices[n.Int64()]
}

func (s RuStore) getAppInfo(packageName string) (map[string]any, error) {
	if appInfo, ok := s.appsCache[packageName]; ok {
		return appInfo, nil
	}
	// If the app info is not in the cache, fetch it from the API
	// and store it in the cache
	url := "https://backapi.rustore.ru/applicationData/overallInfo/" + packageName
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	device := s.getDevice()
	req.Header.Add("User-Agent", "RuStore/1.61.0.2 (Android "+device["firmwareVer"].(string)+"; SDK "+device["androidSdkVer"].(string)+"; arm64-v8a; "+device["deviceModel"].(string)+"; "+device["firmwareLang"].(string)+")")
	req.Header.Add("Connection", "Keep-Alive")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("deviceId", device["deviceId"].(string))
	req.Header.Add("deviceManufacturerName", device["deviceManufacturerName"].(string))
	req.Header.Add("deviceModelName", device["deviceModelName"].(string))
	req.Header.Add("deviceModel", device["deviceModel"].(string))
	req.Header.Add("firmwareLang", device["firmwareLang"].(string))
	req.Header.Add("androidSdkVer", device["androidSdkVer"].(string))
	req.Header.Add("firmwareVer", device["firmwareVer"].(string))
	req.Header.Add("deviceType", device["deviceType"].(string))
	req.Header.Add("ruStoreVerCode", "1061002")
	req.Header.Add("deviceType", "mobile")
	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	defer res.Body.Close()
	body, err := readBody(res)
	if err != nil {
		return nil, err
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
	return nil, nil
}

func (s RuStore) getDownloadLink(appId float64) (string, error) {
	url := "https://backapi.rustore.ru/applicationData/v2/download-link"
	device := s.getDevice()
	sdkVersion, err := strconv.Atoi(device["androidSdkVer"].(string))
	if err != nil {
		return "", err
	}
	payloadData := map[string]any{
		"appId":          appId,
		"firstInstall":   false,
		"mobileServices": []string{"GMS"},
		"supportedAbis": []string{
			"arm64-v8a", "armeabi-v7a", "x86_64", "x86",
		},
		"screenDensity":        480,
		"supportedLocales":     []string{"en_US", "ru_RU"},
		"sdkVersion":           sdkVersion,
		"withoutSplits":        true,
		"signatureFingerprint": nil,
	}
	payloadBytes, err := json.Marshal(payloadData)
	if err != nil {
		return "", err
	}
	payload := bytes.NewReader(payloadBytes)
	req, err := http.NewRequest("POST", url, payload)

	if err != nil {
		return "", err
	}
	req.Header.Add("User-Agent", "RuStore/1.61.0.2 (Android "+device["firmwareVer"].(string)+"; SDK "+device["androidSdkVer"].(string)+"; arm64-v8a; "+device["deviceModel"].(string)+"; "+device["firmwareLang"].(string)+")")
	req.Header.Add("Connection", "Keep-Alive")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("deviceId", device["deviceId"].(string))
	req.Header.Add("deviceManufacturerName", device["deviceManufacturerName"].(string))
	req.Header.Add("deviceModelName", device["deviceModelName"].(string))
	req.Header.Add("deviceModel", device["deviceModel"].(string))
	req.Header.Add("firmwareLang", device["firmwareLang"].(string))
	req.Header.Add("androidSdkVer", device["androidSdkVer"].(string))
	req.Header.Add("firmwareVer", device["firmwareVer"].(string))
	req.Header.Add("deviceType", device["deviceType"].(string))
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

func (s RuStore) FindLatestVersion(packageName string) (Version, error) {
	appInfo, err := s.getAppInfo(packageName)
	if err != nil {
		return Version{}, err
	}
	size := appInfo["fileSize"].(float64)
	versionName := appInfo["versionName"].(string)
	versionCode := appInfo["versionCode"].(float64)
	version := Version{
		Name:        versionName,
		Code:        int64(versionCode),
		Size:        int64(size),
		PackageName: packageName,
	}
	return version, nil
}

func init() {
	s := RuStore{}
	s.appsCache = make(map[string]map[string]any)
	s.devices = []map[string]any{
		{
			"deviceId":               s.generateDeviceId(),
			"deviceManufacturerName": "Google",
			"deviceModelName":        "Pixel 9 Pro",
			"deviceModel":            "Google Pixel 9 Pro",
			"firmwareLang":           "en",
			"androidSdkVer":          "35",
			"firmwareVer":            "15",
			"deviceType":             "mobile",
		},
		{
			"deviceId":               s.generateDeviceId(),
			"deviceManufacturerName": "Google",
			"deviceModelName":        "Pixel 7 Pro",
			"deviceModel":            "Google Pixel 7 Pro",
			"firmwareLang":           "en",
			"androidSdkVer":          "35",
			"firmwareVer":            "15",
			"deviceType":             "mobile",
		}, {
			"deviceId":               s.generateDeviceId(),
			"deviceManufacturerName": "Google",
			"deviceModelName":        "Pixel 9 Pro Fold",
			"deviceModel":            "Google Pixel 9 Pro Fold",
			"firmwareLang":           "en",
			"androidSdkVer":          "35",
			"firmwareVer":            "15",
			"deviceType":             "mobile",
		}, {
			"deviceId":               s.generateDeviceId(),
			"deviceManufacturerName": "Google",
			"deviceModelName":        "Pixel Fold",
			"deviceModel":            "Google Pixel Fold",
			"firmwareLang":           "en",
			"androidSdkVer":          "35",
			"firmwareVer":            "15",
			"deviceType":             "mobile",
		}, {
			"deviceId":               s.generateDeviceId(),
			"deviceManufacturerName": "Google",
			"deviceModelName":        "Pixel 8 Pro",
			"deviceModel":            "Google Pixel 8 Pro",
			"firmwareLang":           "en",
			"androidSdkVer":          "35",
			"firmwareVer":            "15",
			"deviceType":             "mobile",
		}, {
			"deviceId":               s.generateDeviceId(),
			"deviceManufacturerName": "Google",
			"deviceModelName":        "Pixel 9 Pro XL",
			"deviceModel":            "Google Pixel 9 Pro XL",
			"firmwareLang":           "en",
			"androidSdkVer":          "35",
			"firmwareVer":            "15",
			"deviceType":             "mobile",
		}, {
			"deviceId":               s.generateDeviceId(),
			"deviceManufacturerName": "Xiaomi",
			"deviceModelName":        "23127PN0CG",
			"deviceModel":            "Xiaomi 23127PN0CG",
			"firmwareLang":           "en",
			"androidSdkVer":          "35",
			"firmwareVer":            "15",
			"deviceType":             "mobile",
		},
	}
	Register(s)
}
