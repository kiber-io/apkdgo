package sources

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type testProfile struct {
	AppVersion string `yaml:"app_version"`
}

func TestRegisterSourceProfileDecoderWithDefaults(t *testing.T) {
	sourceName := "testprofiledecoderdefaults"
	if err := RegisterSourceProfileDecoder(
		sourceName,
		NewProfileDecoderWithDefaults(
			testProfile{AppVersion: "1.0.0"},
			func(profile *testProfile) {
				profile.AppVersion = strings.TrimSpace(profile.AppVersion)
			},
			func(profile testProfile) error {
				if profile.AppVersion == "" {
					return fmt.Errorf("app_version must be non-empty")
				}
				return nil
			},
		),
	); err != nil {
		t.Fatalf("failed to register source profile decoder: %v", err)
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(`{app_version: " 2.0.0 "}`), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}

	profileAny, err := DecodeSourceProfile(sourceName, node.Content[0])
	if err != nil {
		t.Fatalf("unexpected profile decode error: %v", err)
	}
	profile, ok := profileAny.(testProfile)
	if !ok {
		t.Fatalf("unexpected profile type: %T", profileAny)
	}
	if profile.AppVersion != "2.0.0" {
		t.Fatalf("unexpected profile value: %+v", profile)
	}
}

func TestRegisterSourceProfileDecoderWithDefaultsRejectsUnknownField(t *testing.T) {
	sourceName := "testprofiledecoderunknownfield"
	if err := RegisterSourceProfileDecoder(sourceName, NewProfileDecoderWithDefaults(testProfile{AppVersion: "1.0.0"}, nil, nil)); err != nil {
		t.Fatalf("failed to register source profile decoder: %v", err)
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(`{bad: "x"}`), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}

	if _, err := DecodeSourceProfile(sourceName, node.Content[0]); err == nil {
		t.Fatalf("expected unknown field decode error")
	}
}

func TestRegisterSourceProfileDecoderNormalizesName(t *testing.T) {
	sourceProfileDecodersMu.RLock()
	oldDecoders := sourceProfileDecoders
	sourceProfileDecodersMu.RUnlock()
	t.Cleanup(func() {
		sourceProfileDecodersMu.Lock()
		sourceProfileDecoders = oldDecoders
		sourceProfileDecodersMu.Unlock()
	})

	sourceProfileDecodersMu.Lock()
	sourceProfileDecoders = make(map[string]ProfileDecoder)
	sourceProfileDecodersMu.Unlock()

	if err := RegisterSourceProfileDecoder(" StUb ", NewProfileDecoderWithDefaults(testProfile{AppVersion: "1.0.0"}, nil, nil)); err != nil {
		t.Fatalf("failed to register source profile decoder: %v", err)
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(`{app_version: "2.0.0"}`), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}

	profileAny, err := DecodeSourceProfile(" StUb ", node.Content[0])
	if err != nil {
		t.Fatalf("unexpected profile decode error: %v", err)
	}
	profile, ok := profileAny.(testProfile)
	if !ok {
		t.Fatalf("unexpected profile type: %T", profileAny)
	}
	if profile.AppVersion != "2.0.0" {
		t.Fatalf("unexpected profile value: %+v", profile)
	}
}

func TestResolveSourceProfile(t *testing.T) {
	configuredSourceProfilesMu.RLock()
	oldProfiles := configuredSourceProfiles
	configuredSourceProfilesMu.RUnlock()
	t.Cleanup(func() {
		configuredSourceProfilesMu.Lock()
		configuredSourceProfiles = oldProfiles
		configuredSourceProfilesMu.Unlock()
	})

	defaultProfile := testProfile{AppVersion: "1.0.0"}

	gotDefault, err := ResolveSourceProfile("missing", defaultProfile)
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if gotDefault != defaultProfile {
		t.Fatalf("unexpected default profile: got=%+v expected=%+v", gotDefault, defaultProfile)
	}

	ConfigureSourceProfiles(map[string]any{
		"demo": testProfile{AppVersion: "2.0.0"},
	})
	gotConfigured, err := ResolveSourceProfile("demo", defaultProfile)
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if gotConfigured.AppVersion != "2.0.0" {
		t.Fatalf("unexpected configured profile: %+v", gotConfigured)
	}

	ConfigureSourceProfiles(map[string]any{
		"demo": "wrong-type",
	})
	if _, err := ResolveSourceProfile("demo", defaultProfile); err == nil {
		t.Fatalf("expected resolve error for wrong runtime profile type")
	}
}
