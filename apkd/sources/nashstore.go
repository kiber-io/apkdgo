package sources

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"kiber-io/apkd/apkd/devices"
	"net/http"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type NashStore struct {
	BaseSource
}

func (s NashStore) Name() string {
	return "nashstore"
}

func (s NashStore) answer42() string {
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

func (s NashStore) getAppInfo(packageName string) (map[string]any, error) {
	url := "https://store.nashstore.ru/api/mobile/v1/profile/updates"
	payloadData := map[string]any{
		"apps": map[string]any{
			packageName: map[string]any{
				"appName":          packageName,
				"versionName":      "1.0",
				"firstInstallTime": 1740674665743,
				"lastUpdateTime":   1740698043008,
				"versionCode":      1,
				"packageName":      packageName,
			},
		},
	}
	payloadBytes, err := json.Marshal(payloadData)
	if err != nil {
		return nil, err
	}
	payload := bytes.NewReader(payloadBytes)
	req, err := http.NewRequest("POST", url, payload)

	if err != nil {
		return nil, err
	}
	device := devices.GetRandomDevice()
	caser := cases.Title(language.English)
	deviceBrand := caser.String(device.BuildBrand)
	req.Header.Add("User-Agent", "Nashstore [com.nashstore][0.0.6]["+deviceBrand+"]")
	req.Header.Add("Accept", "application/json, text/plain, */*")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("xaccesstoken", s.answer42())
	appHeader := map[string]any{
		"androidId":   device.GenerateAndroidID(),
		"apiLevel":    device.BuildVersionSdkInt,
		"baseOs":      "",
		"buildId":     device.BuildId,
		"carrier":     "T-Mobile",
		"deviceName":  device.BuildModel,
		"fingerprint": device.BuildFingerprint,
		"fontScale":   1,
		"brand":       device.BuildBrand,
		"deviceId":    device.BuildDevice,
		"width":       device.ScreenWidth,
		"height":      device.ScreenHeight,
		"scale":       2.625,
	}
	appHeaderBytes, err := json.Marshal(appHeader)
	if err != nil {
		return nil, err
	}

	req.Header.Add("nashstore-app", string(appHeaderBytes))

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
		return nil, errors.New("failed to get app info (" + res.Status + "): " + string(body))
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if list, ok := result["list"].([]any); ok {
		if len(list) > 1 {
			return nil, errors.New("multiple apps found")
		}
		if len(list) == 1 {
			appInfo := list[0].(map[string]any)
			return appInfo, nil
		}
	}
	return nil, &AppNotFoundError{PackageName: packageName}
}

func (s NashStore) FindByPackage(packageName string, versionCode int64) (Version, error) {
	appInfo, err := s.getAppInfo(packageName)
	if err != nil {
		return Version{}, err
	}
	appId := appInfo["id"].(string)
	size := appInfo["size"].(float64)
	release := appInfo["release"].(map[string]any)
	versionName := release["version_name"].(string)
	versionCodeApi := release["version_code"].(float64)
	if versionCode != 0 && versionCode != int64(versionCodeApi) {
		return Version{}, &AppNotFoundError{PackageName: packageName}
	}
	link := release["install_path"].(string)
	version := Version{
		Name: versionName,
		Code: int64(versionCode),
		Size: int64(size),
		Link: link,
		// for NashStore developerId is useless because all developer apps are comming in app card
		DeveloperId: appId,
	}
	return version, nil
}

func (s NashStore) Download(version Version) (io.ReadCloser, error) {
	return downloadFile(version.Link)
}

func (s NashStore) MaxParallelsDownloads() int {
	return 3
}

func (s NashStore) FindByDeveloper(developerId string) ([]string, error) {
	// for NashStore developerId == appId
	url := "https://store.nashstore.ru/api/mobile/v1/application/6286364efb3ed3501d52ba65"

	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		return nil, err
	}
	device := devices.GetRandomDevice()
	caser := cases.Title(language.English)
	deviceBrand := caser.String(device.BuildBrand)
	req.Header.Add("User-Agent", "Nashstore [com.nashstore][0.0.6]["+deviceBrand+"]")
	req.Header.Add("Accept", "application/json, text/plain, */*")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("xaccesstoken", s.answer42())
	appHeader := map[string]any{
		"androidId":   device.GenerateAndroidID(),
		"apiLevel":    device.BuildVersionSdkInt,
		"baseOs":      "",
		"buildId":     device.BuildId,
		"carrier":     "T-Mobile",
		"deviceName":  device.BuildModel,
		"fingerprint": device.BuildFingerprint,
		"fontScale":   1,
		"brand":       device.BuildBrand,
		"deviceId":    device.BuildDevice,
		"width":       device.ScreenWidth,
		"height":      device.ScreenHeight,
		"scale":       2.625,
	}
	appHeaderBytes, err := json.Marshal(appHeader)
	if err != nil {
		return nil, err
	}

	req.Header.Add("nashstore-app", string(appHeaderBytes))

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
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("failed to get app info (" + res.Status + "): " + string(body))
	}
	app := result["app"].(map[string]any)
	if app == nil {
		return nil, errors.New("app not found")
	}
	var packages []string
	otherApps := app["other_apps"].(map[string]any)
	if otherApps != nil {
		apps := otherApps["apps"].([]any)
		for _, app := range apps {
			appInfo := app.(map[string]any)
			packages = append(packages, appInfo["app_id"].(string))
		}
	}

	return packages, nil
}

func init() {
	s := NashStore{}
	Register(s)
}
