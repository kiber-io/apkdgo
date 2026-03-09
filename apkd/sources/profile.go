package sources

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type ProfileDecoder func(node *yaml.Node) (any, error)

var sourceProfileDecodersMu sync.RWMutex
var sourceProfileDecoders = make(map[string]ProfileDecoder)

var configuredSourceProfilesMu sync.RWMutex
var configuredSourceProfiles = make(map[string]any)

func RegisterSourceProfileDecoder(sourceName string, decoder ProfileDecoder) error {
	normalizedSourceName := normalizeSourceName(sourceName)
	if normalizedSourceName == "" {
		return fmt.Errorf("source profile decoder name cannot be empty")
	}
	if decoder == nil {
		return fmt.Errorf("source profile decoder cannot be nil")
	}

	sourceProfileDecodersMu.Lock()
	defer sourceProfileDecodersMu.Unlock()
	if _, exists := sourceProfileDecoders[normalizedSourceName]; exists {
		return fmt.Errorf("source profile decoder for %s is already registered", normalizedSourceName)
	}
	sourceProfileDecoders[normalizedSourceName] = decoder
	return nil
}

func buildSourceProfileDecoderWithDefaults[T any](
	defaultProfile T,
	normalize func(*T),
	validate func(T) error,
) ProfileDecoder {
	return func(node *yaml.Node) (any, error) {
		profile := defaultProfile
		if err := DecodeProfileNodeStrict(node, &profile); err != nil {
			return nil, err
		}
		if normalize != nil {
			normalize(&profile)
		}
		if validate != nil {
			if err := validate(profile); err != nil {
				return nil, err
			}
		}
		return profile, nil
	}
}

func NewProfileDecoderWithDefaults[T any](
	defaultProfile T,
	normalize func(*T),
	validate func(T) error,
) ProfileDecoder {
	return buildSourceProfileDecoderWithDefaults(defaultProfile, normalize, validate)
}

func RegisterSourceProfileDecoderWithDefaults[T any](
	sourceName string,
	defaultProfile T,
	normalize func(*T),
	validate func(T) error,
) error {
	return RegisterSourceProfileDecoder(sourceName, buildSourceProfileDecoderWithDefaults(defaultProfile, normalize, validate))
}

func DecodeSourceProfile(sourceName string, node *yaml.Node) (any, error) {
	if node == nil || node.Kind == 0 || node.Tag == "!!null" {
		return nil, nil
	}

	normalizedSourceName := normalizeSourceName(sourceName)
	if normalizedSourceName == "" {
		return nil, fmt.Errorf("source name cannot be empty")
	}

	sourceProfileDecodersMu.RLock()
	decoder, exists := sourceProfileDecoders[normalizedSourceName]
	sourceProfileDecodersMu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("source %s does not support profile settings", normalizedSourceName)
	}

	profile, err := decoder(node)
	if err != nil {
		return nil, err
	}
	return profile, nil
}

func ConfigureSourceProfiles(sourceProfiles map[string]any) {
	normalizedProfiles := make(map[string]any, len(sourceProfiles))
	for sourceName, profile := range sourceProfiles {
		normalizedSourceName := normalizeSourceName(sourceName)
		if normalizedSourceName == "" {
			continue
		}
		normalizedProfiles[normalizedSourceName] = profile
	}

	configuredSourceProfilesMu.Lock()
	configuredSourceProfiles = normalizedProfiles
	configuredSourceProfilesMu.Unlock()
}

func GetConfiguredSourceProfile(sourceName string) (any, bool) {
	normalizedSourceName := normalizeSourceName(sourceName)
	configuredSourceProfilesMu.RLock()
	defer configuredSourceProfilesMu.RUnlock()
	profile, exists := configuredSourceProfiles[normalizedSourceName]
	return profile, exists
}

func ResolveSourceProfile[T any](sourceName string, defaultProfile T) (T, error) {
	normalizedSourceName := normalizeSourceName(sourceName)
	profile, exists := GetConfiguredSourceProfile(normalizedSourceName)
	if !exists {
		return defaultProfile, nil
	}
	typedProfile, ok := profile.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("invalid runtime profile type for source %s: %T", normalizedSourceName, profile)
	}
	return typedProfile, nil
}

func normalizeSourceName(sourceName string) string {
	return strings.ToLower(strings.TrimSpace(sourceName))
}

func DecodeProfileNodeStrict(node *yaml.Node, out any) error {
	if node == nil {
		return fmt.Errorf("profile node cannot be nil")
	}
	var profileYAML bytes.Buffer
	encoder := yaml.NewEncoder(&profileYAML)
	if err := encoder.Encode(node); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}

	decoder := yaml.NewDecoder(&profileYAML)
	decoder.KnownFields(true)
	return decoder.Decode(out)
}
