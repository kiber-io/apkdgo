package sources

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type BaseSourceConfig struct {
	BaseURL string            `yaml:"base_url"`
	Headers map[string]string `yaml:"headers"`
}

type ConfigDecoder func(node *yaml.Node) (any, error)

var sourceConfigDecodersMu sync.RWMutex
var sourceConfigDecoders = make(map[string]ConfigDecoder)

var configuredSourceConfigsMu sync.RWMutex
var configuredSourceConfigs = make(map[string]any)

func RegisterSourceConfigDecoder(sourceName string, decoder ConfigDecoder) error {
	normalizedSourceName := normalizeSourceName(sourceName)
	if normalizedSourceName == "" {
		return errors.New("source config decoder name cannot be empty")
	}
	if decoder == nil {
		return errors.New("source config decoder cannot be nil")
	}

	sourceConfigDecodersMu.Lock()
	defer sourceConfigDecodersMu.Unlock()
	if _, exists := sourceConfigDecoders[normalizedSourceName]; exists {
		return fmt.Errorf("source config decoder for %s is already registered", normalizedSourceName)
	}
	sourceConfigDecoders[normalizedSourceName] = decoder
	return nil
}

func buildSourceConfigDecoderWithDefaults[T any](
	defaultConfig T,
	normalize func(*T),
	validate func(T) error,
) ConfigDecoder {
	return func(node *yaml.Node) (any, error) {
		config := defaultConfig
		if err := DecodeConfigNodeStrict(node, &config); err != nil {
			return nil, err
		}
		if normalize != nil {
			normalize(&config)
		}
		if validate != nil {
			if err := validate(config); err != nil {
				return nil, err
			}
		}
		return config, nil
	}
}

func NewConfigDecoderWithDefaults[T any](
	defaultConfig T,
	normalize func(*T),
	validate func(T) error,
) ConfigDecoder {
	return buildSourceConfigDecoderWithDefaults(defaultConfig, normalize, validate)
}

func RegisterSourceConfigDecoderWithDefaults[T any](
	sourceName string,
	defaultConfig T,
	normalize func(*T),
	validate func(T) error,
) error {
	return RegisterSourceConfigDecoder(sourceName, buildSourceConfigDecoderWithDefaults(defaultConfig, normalize, validate))
}

// ErrNoConfig is returned when a source has no config configured.
var ErrNoConfig = errors.New("no config configured")

func DecodeSourceConfig(sourceName string, node *yaml.Node) (any, error) {
	if node == nil || node.Kind == 0 || node.Tag == "!!null" {
		return nil, ErrNoConfig
	}

	normalizedSourceName := normalizeSourceName(sourceName)
	if normalizedSourceName == "" {
		return nil, errors.New("source name cannot be empty")
	}

	sourceConfigDecodersMu.RLock()
	decoder, exists := sourceConfigDecoders[normalizedSourceName]
	sourceConfigDecodersMu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("source %s does not support config settings", normalizedSourceName)
	}

	config, err := decoder(node)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func ConfigureSourceConfigs(sourceConfigs map[string]any) {
	normalizedConfigs := make(map[string]any, len(sourceConfigs))
	for sourceName, config := range sourceConfigs {
		normalizedSourceName := normalizeSourceName(sourceName)
		if normalizedSourceName == "" {
			continue
		}
		normalizedConfigs[normalizedSourceName] = config
	}

	configuredSourceConfigsMu.Lock()
	configuredSourceConfigs = normalizedConfigs
	configuredSourceConfigsMu.Unlock()
}

func GetConfiguredSourceConfig(sourceName string) (any, bool) {
	normalizedSourceName := normalizeSourceName(sourceName)
	configuredSourceConfigsMu.RLock()
	defer configuredSourceConfigsMu.RUnlock()
	config, exists := configuredSourceConfigs[normalizedSourceName]
	return config, exists
}

func ResolveSourceConfig[T any](sourceName string, defaultConfig T) (T, error) {
	normalizedSourceName := normalizeSourceName(sourceName)
	config, exists := GetConfiguredSourceConfig(normalizedSourceName)
	if !exists {
		return defaultConfig, nil
	}
	typedConfig, ok := config.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("invalid runtime config type for source %s: %T", normalizedSourceName, config)
	}
	return typedConfig, nil
}

func normalizeSourceName(sourceName string) string {
	return strings.ToLower(strings.TrimSpace(sourceName))
}

func DecodeConfigNodeStrict(node *yaml.Node, out any) error {
	if node == nil {
		return errors.New("config node cannot be nil")
	}
	var configYAML bytes.Buffer
	encoder := yaml.NewEncoder(&configYAML)
	if err := encoder.Encode(node); err != nil {
		return fmt.Errorf("failed to encode config YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to close YAML encoder: %w", err)
	}

	decoder := yaml.NewDecoder(&configYAML)
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("failed to decode config: %w", err)
	}
	return nil
}

func NormalizeBaseSourceConfig(config *BaseSourceConfig) {
	if config == nil {
		return
	}
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if len(config.Headers) == 0 {
		return
	}
	normalizedHeaders := make(map[string]string, len(config.Headers))
	for headerName, headerValue := range config.Headers {
		normalizedHeaderName := http.CanonicalHeaderKey(strings.TrimSpace(headerName))
		normalizedHeaders[normalizedHeaderName] = strings.TrimSpace(headerValue)
	}
	config.Headers = normalizedHeaders
}

func ValidateBaseSourceConfig(config BaseSourceConfig) error {
	for headerName := range config.Headers {
		if strings.TrimSpace(headerName) == "" {
			return errors.New("headers contains an empty header name")
		}
	}
	return nil
}

func ApplyConfiguredHeaders(baseHeaders http.Header, configuredHeaders map[string]string) http.Header {
	resolvedHeaders := cloneHTTPHeaders(baseHeaders)
	for headerName, headerValue := range configuredHeaders {
		resolvedHeaders.Set(headerName, headerValue)
	}
	return resolvedHeaders
}

func cloneHTTPHeaders(headers http.Header) http.Header {
	if headers == nil {
		return http.Header{}
	}
	cloned := make(http.Header, len(headers))
	for headerName, values := range headers {
		canonicalHeaderName := http.CanonicalHeaderKey(headerName)
		cloned[canonicalHeaderName] = append(cloned[canonicalHeaderName], values...)
	}
	return cloned
}
