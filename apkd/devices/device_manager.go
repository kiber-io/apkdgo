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
	BuildBrand          string    `properties:"Build.BRAND"`
	BuildVersionSdkInt  int       `properties:"Build.VERSION.SDK_INT"`
	BuildModel          string    `properties:"Build.MODEL"`
	CellOperator        int       `properties:"CellOperator"`
	BuildFingerprint    string    `properties:"Build.FINGERPRINT"`
	BuildDevice         string    `properties:"Build.DEVICE"`
	BuildProduct        string    `properties:"Build.PRODUCT"`
	ScreenHeight        int       `properties:"Screen.Height"`
	ScreenWidth         int       `properties:"Screen.Width"`
	ScreenDensity       int       `properties:"Screen.Density"`
	Platforms           PropArray `properties:"Platforms"`
	SimOperator         int       `properties:"SimOperator"`
	Features            PropArray `properties:"Features"`
	BuildRadio          string    `properties:"Build.RADIO"`
	BuildId             string    `properties:"Build.ID"`
	BuildVersionRelease string    `properties:"Build.VERSION.RELEASE"`
	BuildBootloader     string    `properties:"Build.BOOTLOADER"`
	BuildManufacturer   string    `properties:"Build.MANUFACTURER"`
	BuildHardware       string    `properties:"Build.HARDWARE"`
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
