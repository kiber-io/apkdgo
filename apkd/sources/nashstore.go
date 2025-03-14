package sources

import (
	"bytes"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	mrand "math/rand"
	"net/http"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type NashStore struct {
	devices []map[string]any
}

func (s NashStore) Name() string {
	return "nashstore"
}

func (s NashStore) getDevice() map[string]any {
	n, err := crand.Int(crand.Reader, big.NewInt(int64(len(s.devices))))
	if err != nil {
		// Fallback to math/rand
		return s.devices[mrand.Intn(len(s.devices))]
	}
	return s.devices[n.Int64()]
}

func (s NashStore) generateAndroidID() string {
	bytes := make([]byte, 8)
	if _, err := crand.Read(bytes); err != nil {
		// Fallback to math/rand if crypto/rand fails
		for i := range bytes {
			bytes[i] = byte(mrand.Intn(256))
		}
	}
	return hex.EncodeToString(bytes)
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
	device := s.getDevice()
	caser := cases.Title(language.English)
	deviceBrand := caser.String(device["brand"].(string))
	req.Header.Add("User-Agent", "Nashstore [com.nashstore][0.0.6]["+deviceBrand)
	req.Header.Add("Accept", "application/json, text/plain, */*")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("xaccesstoken", s.answer42())
	appHeaderBytes, err := json.Marshal(device)
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
	return nil, nil
}

func (s NashStore) FindLatestVersion(packageName string) (Version, error) {
	appInfo, err := s.getAppInfo(packageName)
	if err != nil {
		return Version{}, err
	}
	size := appInfo["size"].(float64)
	release := appInfo["release"].(map[string]any)
	versionName := release["version_name"].(string)
	versionCode := release["version_code"].(float64)
	link := release["install_path"].(string)
	version := Version{
		Name: versionName,
		Code: int64(versionCode),
		Size: int64(size),
		Link: link,
	}
	return version, nil
}

func (s NashStore) Download(version Version) (io.ReadCloser, error) {
	return downloadFile(version.Link)
}

func (s NashStore) generateRandomScreenWidth() float64 {
	return 360 + mrand.Float64()*(480-360)
}

func (s NashStore) generateRandomScreenHeigth() float64 {
	return 640 + mrand.Float64()*(1280-640)
}

func (s NashStore) generateRandomScale() float64 {
	return 2.0 + mrand.Float64()*(4.0-2.0)
}

func init() {
	s := NashStore{}
	s.devices = []map[string]any{
		{
			"androidId":   s.generateAndroidID(),
			"apiLevel":    35,
			"baseOs":      "",
			"buildId":     "AP4A.250205.002",
			"carrier":     "MTS",
			"deviceName":  "Pixel 7",
			"fingerprint": "google/panther/panther:15/AP4A.250205.002/12821496:user/release-keys",
			"fontScale":   1,
			"brand":       "google",
			"deviceId":    "panther",
			"width":       s.generateRandomScreenWidth(),
			"height":      s.generateRandomScreenHeigth(),
			"scale":       s.generateRandomScale(),
		},
		{
			"androidId":   s.generateAndroidID(),
			"apiLevel":    35,
			"baseOs":      "",
			"buildId":     "BP1A.250305.020",
			"carrier":     "Viva",
			"deviceName":  "Pixel 9 Pro",
			"fingerprint": "google/caiman/caiman:15/BP1A.250305.020/13009785:user/release-keys",
			"fontScale":   1,
			"brand":       "google",
			"deviceId":    "caiman",
			"width":       s.generateRandomScreenWidth(),
			"height":      s.generateRandomScreenHeigth(),
			"scale":       s.generateRandomScale(),
		},
		{
			"androidId":   s.generateAndroidID(),
			"apiLevel":    35,
			"baseOs":      "",
			"buildId":     "BP1A.250305.019",
			"carrier":     "MegaFon",
			"deviceName":  "Pixel 7 Pro",
			"fingerprint": "google/cheetah/cheetah:15/BP1A.250305.019/13003188:user/release-keys",
			"fontScale":   1,
			"brand":       "google",
			"deviceId":    "cheetah",
			"width":       s.generateRandomScreenWidth(),
			"height":      s.generateRandomScreenHeigth(),
			"scale":       s.generateRandomScale(),
		},
		{
			"androidId":   s.generateAndroidID(),
			"apiLevel":    35,
			"baseOs":      "",
			"buildId":     "BP1A.250305.020",
			"carrier":     "MTS",
			"deviceName":  "Pixel 9 Pro Fold",
			"fingerprint": "google/comet/comet:15/BP1A.250305.020/13009785:user/release-keys",
			"fontScale":   1,
			"brand":       "google",
			"deviceId":    "comet",
			"width":       s.generateRandomScreenWidth(),
			"height":      s.generateRandomScreenHeigth(),
			"scale":       s.generateRandomScale(),
		},
		{
			"androidId":   s.generateAndroidID(),
			"apiLevel":    35,
			"baseOs":      "",
			"buildId":     "BP1A.250305.019",
			"carrier":     "MegaFon",
			"deviceName":  "Pixel Fold",
			"fingerprint": "google/felix/felix:15/BP1A.250305.019/13003188:user/release-keys",
			"fontScale":   1,
			"brand":       "google",
			"deviceId":    "felix",
			"width":       s.generateRandomScreenWidth(),
			"height":      s.generateRandomScreenHeigth(),
			"scale":       s.generateRandomScale(),
		},
	}
	Register(s)
}
