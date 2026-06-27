package sources

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type testConfig struct {
	BaseSourceConfig `yaml:",inline"`
	AppVersion       string `yaml:"app_version"`
}

func TestRegisterSourceConfigDecoderWithDefaults(t *testing.T) {
	sourceName := "testconfigdecoderdefaults"
	if err := RegisterSourceConfigDecoder(
		sourceName,
		NewConfigDecoderWithDefaults(
			testConfig{AppVersion: "1.0.0"},
			func(config *testConfig) {
				NormalizeBaseSourceConfig(&config.BaseSourceConfig)
				config.AppVersion = strings.TrimSpace(config.AppVersion)
			},
			func(config testConfig) error {
				if err := ValidateBaseSourceConfig(config.BaseSourceConfig); err != nil {
					return err
				}
				if config.AppVersion == "" {
					return errors.New("app_version must be non-empty")
				}
				return nil
			},
		),
	); err != nil {
		t.Fatalf("failed to register source config decoder: %v", err)
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(`{app_version: " 2.0.0 ", headers: {" user-agent ": " test "}}`), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}

	configAny, err := DecodeSourceConfig(sourceName, node.Content[0])
	if err != nil {
		t.Fatalf("unexpected config decode error: %v", err)
	}
	config, ok := configAny.(testConfig)
	if !ok {
		t.Fatalf("unexpected config type: %T", configAny)
	}
	if config.AppVersion != "2.0.0" {
		t.Fatalf("unexpected config value: %+v", config)
	}
	if config.Headers["User-Agent"] != "test" {
		t.Fatalf("unexpected headers normalization: %+v", config.Headers)
	}
}

func TestRegisterSourceConfigDecoderWithDefaultsRejectsUnknownField(t *testing.T) {
	sourceName := "testconfigdecoderunknownfield"
	if err := RegisterSourceConfigDecoder(sourceName, NewConfigDecoderWithDefaults(testConfig{AppVersion: "1.0.0"}, nil, nil)); err != nil {
		t.Fatalf("failed to register source config decoder: %v", err)
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(`{bad: "x"}`), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}

	if _, err := DecodeSourceConfig(sourceName, node.Content[0]); err == nil {
		t.Fatalf("expected unknown field decode error")
	}
}

func TestRegisterSourceConfigDecoderNormalizesName(t *testing.T) {
	sourceConfigDecodersMu.RLock()
	oldDecoders := sourceConfigDecoders
	sourceConfigDecodersMu.RUnlock()
	t.Cleanup(func() {
		sourceConfigDecodersMu.Lock()
		sourceConfigDecoders = oldDecoders
		sourceConfigDecodersMu.Unlock()
	})

	sourceConfigDecodersMu.Lock()
	sourceConfigDecoders = make(map[string]ConfigDecoder)
	sourceConfigDecodersMu.Unlock()

	if err := RegisterSourceConfigDecoder(" StUb ", NewConfigDecoderWithDefaults(testConfig{AppVersion: "1.0.0"}, nil, nil)); err != nil {
		t.Fatalf("failed to register source config decoder: %v", err)
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(`{app_version: "2.0.0"}`), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}

	configAny, err := DecodeSourceConfig(" StUb ", node.Content[0])
	if err != nil {
		t.Fatalf("unexpected config decode error: %v", err)
	}
	config, ok := configAny.(testConfig)
	if !ok {
		t.Fatalf("unexpected config type: %T", configAny)
	}
	if config.AppVersion != "2.0.0" {
		t.Fatalf("unexpected config value: %+v", config)
	}
}

func TestResolveSourceConfig(t *testing.T) {
	configuredSourceConfigsMu.RLock()
	oldConfigs := configuredSourceConfigs
	configuredSourceConfigsMu.RUnlock()
	t.Cleanup(func() {
		configuredSourceConfigsMu.Lock()
		configuredSourceConfigs = oldConfigs
		configuredSourceConfigsMu.Unlock()
	})

	defaultConfig := testConfig{AppVersion: "1.0.0"}

	gotDefault, err := ResolveSourceConfig("missing", defaultConfig)
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if !reflect.DeepEqual(gotDefault, defaultConfig) {
		t.Fatalf("unexpected default config: got=%+v expected=%+v", gotDefault, defaultConfig)
	}

	ConfigureSourceConfigs(map[string]any{
		"demo": testConfig{AppVersion: "2.0.0"},
	})
	gotConfigured, err := ResolveSourceConfig("demo", defaultConfig)
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if gotConfigured.AppVersion != "2.0.0" {
		t.Fatalf("unexpected configured config: %+v", gotConfigured)
	}

	ConfigureSourceConfigs(map[string]any{
		"demo": "wrong-type",
	})
	if _, err := ResolveSourceConfig("demo", defaultConfig); err == nil {
		t.Fatalf("expected resolve error for wrong runtime config type")
	}
}

func TestApplyConfiguredHeadersCanonicalizesBaseHeaders(t *testing.T) {
	headers := ApplyConfiguredHeaders(http.Header{
		"ruStoreVerCode": {"1103100"},
	}, map[string]string{
		"Rustorevercode": "1093002",
	})

	if len(headers) != 1 {
		t.Fatalf("expected one canonicalized header entry, got %+v", headers)
	}
	if got := headers.Get("ruStoreVerCode"); got != "1093002" {
		t.Fatalf("expected configured header to replace base value, got %q", got)
	}
}
