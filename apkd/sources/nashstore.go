package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"kiber-io/apkd/apkd/devices"
	"net/http"

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
	Size        uint64
}

type AppInfoNashStore struct {
	App AppNashStore `json:"app"`
}

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

func (s NashStore) getAppInfo(packageName string) (AppInfoNashStore, error) {
	var appInfo AppInfoNashStore
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
		return appInfo, err
	}
	payload := bytes.NewReader(payloadBytes)
	req, err := http.NewRequest("POST", url, payload)

	if err != nil {
		return appInfo, err
	}
	device := devices.GetRandomDevice()
	caser := cases.Title(language.English)
	deviceBrand := caser.String(device.BuildBrand)
	req.Header.Add("User-Agent", "Nashstore [com.nashstore][0.0.6]["+deviceBrand)
	req.Header.Add("Accept", "application/json, text/plain, */*")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("Content-Type", "application/json")
	tok := s.answer42()
	req.Header.Add("xaccesstoken", tok)
	req.Header.Add("Cookie", "nashstore_token="+tok)
	appHeader := map[string]any{
		"androidId":   device.GenerateAndroidID(),
		"apiLevel":    device.BuildVersionSdkInt,
		"baseOs":      "",
		"buildId":     device.BuildId,
		"carrier":     "MTS",
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
		return appInfo, err
	}

	req.Header.Add("nashstore-app", string(appHeaderBytes))

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return appInfo, err
	}

	defer res.Body.Close()
	body, err := readBody(res)
	if err != nil {
		return appInfo, err
	}
	if res.StatusCode != http.StatusOK {
		return appInfo, fmt.Errorf("failed to get app info (" + res.Status + "): " + string(body))
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return appInfo, err
	}
	if list, ok := result["list"].([]any); ok {
		if len(list) > 1 {
			return appInfo, fmt.Errorf("multiple apps found")
		} else if len(list) == 0 {
			return appInfo, &AppNotFoundError{PackageName: packageName}
		}
		if len(list) == 1 {
			jsonAppInfo, err := json.Marshal(list[0])
			if err != nil {
				return appInfo, err
			}
			if err := json.Unmarshal(jsonAppInfo, &appInfo.App); err != nil {
				return appInfo, err
			}
			appInfo2 := list[0].(map[string]any)
			appInfo.App.Size = uint64(appInfo2["size"].(float64))
			return appInfo, nil
		}
	}
	return appInfo, fmt.Errorf("failed to parse app info")
}

func (s NashStore) FindByPackage(packageName string, versionCode int) (Version, error) {
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

func (s NashStore) Download(version Version) (io.ReadCloser, error) {
	return downloadFile(version.Link)
}

func (s NashStore) MaxParallelsDownloads() int {
	return 3
}

func (s NashStore) FindByDeveloper(developerId string) ([]Version, error) {
	url := "https://store.nashstore.ru/api/mobile/v1/application/" + developerId

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
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get app info (" + res.Status + "): " + string(body))
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	app := result["app"].(map[string]any)
	if app == nil {
		return nil, fmt.Errorf("app not found")
	}
	var versions []Version
	otherApps := app["other_apps"].(map[string]any)
	if otherApps != nil {
		// cut first app because it is the same as the requested app
		// and we need only other apps
		apps := otherApps["apps"].([]any)[1:]
		for _, app := range apps {
			appJson, err := json.Marshal(app)
			if err != nil {
				return nil, err
			}
			var appInfo AppNashStore
			if err := json.Unmarshal(appJson, &appInfo); err != nil {
				return nil, err
			}
			version := Version{
				Name:        appInfo.Release.VersionName,
				Code:        appInfo.Release.VersionCode,
				Size:        appInfo.Size,
				PackageName: appInfo.PackageName,
				DeveloperId: appInfo.Id,
				Link:        appInfo.Release.Link,
				Type:        APK,
			}
			versions = append(versions, version)
		}
	}

	return versions, nil
}

func init() {
	s := NashStore{}
	Register(s)
}
