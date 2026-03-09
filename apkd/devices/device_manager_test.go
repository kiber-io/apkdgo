package devices

import (
	"encoding/hex"
	"reflect"
	"testing"
)

func TestPropArrayUnmarshalText(t *testing.T) {
	var arr PropArray
	if err := arr.UnmarshalText([]byte("arm64-v8a,x86_64")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(arr) != 2 || arr[0] != "arm64-v8a" || arr[1] != "x86_64" {
		t.Fatalf("unexpected parsed value: %#v", arr)
	}
}

func TestGenerateAndroidID(t *testing.T) {
	d := Device{}
	id := d.GenerateAndroidID()
	if len(id) != 16 {
		t.Fatalf("expected android id length 16, got %d", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("expected hex-encoded android id, got %q: %v", id, err)
	}
}

func TestGetRandomDeviceReturnsKnownDevice(t *testing.T) {
	if len(devices) == 0 {
		t.Fatalf("expected embedded devices list to be non-empty")
	}

	got := GetRandomDevice()
	found := false
	for _, d := range devices {
		if reflect.DeepEqual(d, got) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("returned device is not present in loaded device list")
	}
}
