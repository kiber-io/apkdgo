package devices

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"math/rand"
	"time"
)

type Device struct {
	Brand          string
	Manufacturer   string
	Model          string
	Device         string
	Product        string
	AndroidVersion string
	SDKInt         int
	DPI            int
	Width          int
	Height         int
	Density        string
	CPUAbis        []string
	BuildID        string
	Fingerprint    string
	AndroidID      string
}

type sdkInfo struct {
	sdk     int
	android string
}

var sdks = []sdkInfo{
	{33, "13"},
	{34, "14"},
	{35, "15"},
	{36, "16"},
}

var dpis = []int{
	320,
	360,
	400,
	420,
	440,
	480,
}

var buildIDs = []string{
	"TQ3A.230805.001",
	"UP1A.231005.007",
	"AP1A.240205.002",
}

var templates = []Device{
	{
		Brand:        "google",
		Manufacturer: "Google",
		Model:        "Pixel 7",
		Device:       "panther",
		Product:      "panther",
		CPUAbis:      []string{"arm64-v8a", "armeabi-v7a"},
		Width:        1080,
		Height:       2400,
	},
	{
		Brand:        "google",
		Manufacturer: "Google",
		Model:        "Pixel 6",
		Device:       "oriole",
		Product:      "oriole",
		CPUAbis:      []string{"arm64-v8a", "armeabi-v7a"},
		Width:        1080,
		Height:       2400,
	},
	{
		Brand:        "samsung",
		Manufacturer: "Samsung",
		Model:        "SM-G991B",
		Device:       "o1s",
		Product:      "o1sxx",
		CPUAbis:      []string{"arm64-v8a", "armeabi-v7a"},
		Width:        1080,
		Height:       2400,
	},
	{
		Brand:        "samsung",
		Manufacturer: "Samsung",
		Model:        "SM-S911B",
		Device:       "dm1q",
		Product:      "dm1qxx",
		CPUAbis:      []string{"arm64-v8a", "armeabi-v7a"},
		Width:        1080,
		Height:       2340,
	},
	{
		Brand:        "xiaomi",
		Manufacturer: "Xiaomi",
		Model:        "2201123G",
		Device:       "ingres",
		Product:      "ingres_global",
		CPUAbis:      []string{"arm64-v8a", "armeabi-v7a"},
		Width:        1080,
		Height:       2400,
	},
}

func RandomDevice() Device {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	tpl := templates[rng.Intn(len(templates))]
	sdk := sdks[rng.Intn(len(sdks))]
	dpi := dpis[rng.Intn(len(dpis))]
	build := buildIDs[rng.Intn(len(buildIDs))]

	tpl.SDKInt = sdk.sdk
	tpl.AndroidVersion = sdk.android
	tpl.DPI = dpi
	tpl.Density = densityBucket(dpi)

	tpl.BuildID = build
	tpl.Fingerprint = buildFingerprint(tpl)
	tpl.AndroidID = generateAndroidID()

	return tpl
}

func densityBucket(dpi int) string {
	switch {
	case dpi <= 320:
		return "xhdpi"
	case dpi <= 400:
		return "xxhdpi"
	default:
		return "xxxhdpi"
	}
}

func buildFingerprint(d Device) string {
	return fmt.Sprintf(
		"%s/%s/%s:%s/%s:user/release-keys",
		d.Brand,
		d.Product,
		d.Device,
		d.AndroidVersion,
		d.BuildID,
	)
}

func generateAndroidID() string {
	randomBytes := make([]byte, 8)
	if _, err := crand.Read(randomBytes); err != nil {
		fallback := rand.New(rand.NewSource(time.Now().UnixNano()))
		for i := range randomBytes {
			randomBytes[i] = byte(fallback.Intn(256))
		}
	}
	return hex.EncodeToString(randomBytes)
}
