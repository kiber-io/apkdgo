package devices

import (
	crand "crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math/big"
	mrand "math/rand"
	"os"
	"strings"

	"github.com/kiber-io/properties"
)

type PropArray []string

func (s *PropArray) UnmarshalText(text []byte) error {
	*s = strings.Split(string(text), ",")
	return nil
}

var devices = []Device{}

//go:embed configs/*.properties
var configs embed.FS

type Device struct {
	BuildBrand          string    `properties:"Build.BRAND,default="`
	BuildVersionSdkInt  int       `properties:"Build.VERSION.SDK_INT,default=0"`
	BuildModel          string    `properties:"Build.MODEL,default="`
	CellOperator        int       `properties:"CellOperator,default=0"`
	BuildFingerprint    string    `properties:"Build.FINGERPRINT,default="`
	BuildDevice         string    `properties:"Build.DEVICE,default="`
	BuildProduct        string    `properties:"Build.PRODUCT,default="`
	ScreenHeight        int       `properties:"Screen.Height,default=0"`
	ScreenWidth         int       `properties:"Screen.Width,default=0"`
	ScreenDensity       int       `properties:"Screen.Density,default=0"`
	Platforms           PropArray `properties:"Platforms,default=arm64-v8a,armeabi-v7a,x86,x86_64"`
	SimOperator         int       `properties:"SimOperator,default=0"`
	Features            PropArray `properties:"Features,default="`
	BuildRadio          string    `properties:"Build.RADIO,default="`
	BuildId             string    `properties:"Build.ID,default="`
	BuildVersionRelease string    `properties:"Build.VERSION.RELEASE,default=0"`
	BuildBootloader     string    `properties:"Build.BOOTLOADER,default="`
	BuildManufacturer   string    `properties:"Build.MANUFACTURER,default="`
	BuildHardware       string    `properties:"Build.HARDWARE,default="`
}

func (s *Device) GenerateAndroidID() string {
	bytes := make([]byte, 8)
	if _, err := crand.Read(bytes); err != nil {
		// Fallback to math/rand if crypto/rand fails
		for i := range bytes {
			bytes[i] = byte(mrand.Intn(256))
		}
	}
	return hex.EncodeToString(bytes)
}

func GetRandomDevice() Device {
	n, err := crand.Int(crand.Reader, big.NewInt(int64(len(devices))))
	if err != nil {
		// Fallback to math/rand
		return devices[mrand.Intn(len(devices))]
	}
	return devices[n.Int64()]
}

func loadProperties() error {
	err := fs.WalkDir(configs, "configs", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}
		data, err := configs.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		props, err := properties.Load(data, properties.UTF8)
		if err != nil {
			return fmt.Errorf("failed to load %s: %w", path, err)
		}

		var device Device
		if err := props.Decode(&device); err != nil {
			return fmt.Errorf("failed to decode %s: %w", path, err)
		}
		devices = append(devices, device)
		return nil
	})

	return err
}

func init() {
	err := loadProperties()
	if err != nil {
		fmt.Printf("Error loading devices configs: %v\n", err)
		os.Exit(1)
	}
}
