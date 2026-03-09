package devices

import (
	"regexp"
	"testing"
)

var androidIDFormatRegexp = regexp.MustCompile("^[0-9a-f]{16}$")

func TestGenerateAndroidIDFormat(t *testing.T) {
	id := generateAndroidID()
	if !androidIDFormatRegexp.MatchString(id) {
		t.Fatalf("unexpected android id format: %q", id)
	}
}

func TestRandomDeviceContainsAndroidID(t *testing.T) {
	device := RandomDevice()
	if !androidIDFormatRegexp.MatchString(device.AndroidID) {
		t.Fatalf("unexpected android id in device: %q", device.AndroidID)
	}
}
