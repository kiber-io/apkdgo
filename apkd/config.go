package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	Version  int                     `yaml:"version"`
	Defaults ConfigDefaults          `yaml:"defaults"`
	Runtime  ConfigRuntime           `yaml:"runtime"`
	Network  ConfigNetwork           `yaml:"network"`
	Sources  map[string]SourceConfig `yaml:"sources"`
}

const (
	defaultConfigVersion   = 1
	defaultConfigVerbosity = 1
	defaultConfigWorkers   = 3
)

var builtInDefaultConfig = newBuiltInDefaultConfig()

func newBuiltInDefaultConfig() *AppConfig {
	return &AppConfig{
		Version: defaultConfigVersion,
		Defaults: ConfigDefaults{
			Verbose: valuePointer(defaultConfigVerbosity),
		},
		Runtime: ConfigRuntime{
			Workers: valuePointer(defaultConfigWorkers),
		},
	}
}

func cloneBuiltInDefaultConfig() *AppConfig {
	cloned := *builtInDefaultConfig
	cloned.Defaults = builtInDefaultConfig.Defaults
	cloned.Runtime = builtInDefaultConfig.Runtime
	cloned.Defaults.Verbose = valuePointer(*builtInDefaultConfig.Defaults.Verbose)
	cloned.Runtime.Workers = valuePointer(*builtInDefaultConfig.Runtime.Workers)
	return &cloned
}

func valuePointer[T any](value T) *T {
	return &value
}

func valueOrZero[T any](value *T) T {
	var zero T
	if value == nil {
		return zero
	}
	return *value
}

type ConfigDefaults struct {
	Sources   []string `yaml:"sources"`
	OutputDir *string  `yaml:"output_dir"`
	Force     *bool    `yaml:"force"`
	Dev       *bool    `yaml:"dev"`
	Verbose   *int     `yaml:"verbose"`
}

type ConfigRuntime struct {
	Workers *int `yaml:"workers"`
}

type ConfigNetwork struct {
	Timeout *time.Duration `yaml:"timeout"`
	Retry   ConfigRetry    `yaml:"retry"`
	Proxy   ConfigProxy    `yaml:"proxy"`
}

type ConfigRetry struct {
	MaxAttempts *int  `yaml:"max_attempts"`
	DelayMs     *int  `yaml:"delay_ms"`
	MaxDelayMs  *int  `yaml:"max_delay_ms"`
	RetryStatus []int `yaml:"retry_status"`
}

func (r ConfigRetry) IsSet() bool {
	return r.MaxAttempts != nil || r.DelayMs != nil || r.MaxDelayMs != nil || len(r.RetryStatus) > 0
}

type ConfigProxy struct {
	Global             *string           `yaml:"global"`
	InsecureSkipVerify *bool             `yaml:"insecure_skip_verify"`
	PerSource          map[string]string `yaml:"per_source"`
}

type SourceConfig struct {
	Profile *SourceProfileNode `yaml:"profile"`
	Headers map[string]string  `yaml:"headers"`
}

type SourceProfileNode struct {
	Node *yaml.Node
}

func (n *SourceProfileNode) UnmarshalYAML(value *yaml.Node) error {
	n.Node = value
	return nil
}

func defaultConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, "apkd", "config.yml"), nil
}

func resolveConfigPath(path string) (string, error) {
	normalizedPath := strings.TrimSpace(path)
	if normalizedPath != "" {
		absPath, err := filepath.Abs(normalizedPath)
		if err != nil {
			return "", fmt.Errorf("failed to get absolute path for config file: %w", err)
		}
		return absPath, nil
	}

	fallbackPath, err := defaultConfigPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(fallbackPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("failed to check default config file %s: %w", fallbackPath, err)
	}
	return fallbackPath, nil
}

func loadConfig(path string) (*AppConfig, error) {
	cfg := cloneBuiltInDefaultConfig()
	normalizedPath := strings.TrimSpace(path)
	if normalizedPath == "" {
		if err := normalizeConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	file, err := os.Open(normalizedPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			if err := normalizeConfig(cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, err
	}
	var extraDoc any
	if err := decoder.Decode(&extraDoc); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple YAML documents are not supported")
		}
		return nil, err
	}
	if err := normalizeConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func normalizeConfig(cfg *AppConfig) error {
	if cfg.Version == 0 {
		cfg.Version = defaultConfigVersion
	}
	if cfg.Version != defaultConfigVersion {
		return fmt.Errorf("unsupported config version %d", cfg.Version)
	}

	if cfg.Defaults.Verbose != nil && *cfg.Defaults.Verbose < 0 {
		return fmt.Errorf("defaults.verbose must be >= 0")
	}
	if cfg.Runtime.Workers != nil && *cfg.Runtime.Workers <= 0 {
		return fmt.Errorf("runtime.workers must be > 0")
	}
	if cfg.Network.Timeout != nil && *cfg.Network.Timeout <= 0 {
		return fmt.Errorf("network.timeout must be > 0")
	}

	if cfg.Defaults.OutputDir != nil {
		outputDir := strings.TrimSpace(*cfg.Defaults.OutputDir)
		outputDir, err := filepath.Abs(outputDir)
		if err != nil {
			return fmt.Errorf("error getting absolute path for output directory %s: %v", outputDir, err)
		}
		cfg.Defaults.OutputDir = &outputDir
	}
	if cfg.Network.Proxy.Global != nil {
		proxyURL := strings.TrimSpace(*cfg.Network.Proxy.Global)
		cfg.Network.Proxy.Global = &proxyURL
	}

	normalizedSelectedSources := make([]string, 0, len(cfg.Defaults.Sources))
	for _, sourceName := range cfg.Defaults.Sources {
		normalizedSourceName := strings.ToLower(strings.TrimSpace(sourceName))
		if normalizedSourceName == "" {
			return fmt.Errorf("defaults.sources contains an empty source name")
		}
		normalizedSelectedSources = append(normalizedSelectedSources, normalizedSourceName)
	}
	cfg.Defaults.Sources = normalizedSelectedSources

	normalizedProxyBySource := make(map[string]string, len(cfg.Network.Proxy.PerSource))
	for sourceName, rawProxyURL := range cfg.Network.Proxy.PerSource {
		normalizedSourceName := strings.ToLower(strings.TrimSpace(sourceName))
		normalizedProxyURL := strings.TrimSpace(rawProxyURL)
		if normalizedSourceName == "" {
			return fmt.Errorf("network.proxy.per_source contains an empty source name")
		}
		if normalizedProxyURL == "" {
			return fmt.Errorf("network.proxy.per_source[%s] must be non-empty", normalizedSourceName)
		}
		normalizedProxyBySource[normalizedSourceName] = normalizedProxyURL
	}
	cfg.Network.Proxy.PerSource = normalizedProxyBySource

	normalizedSourceCfg := make(map[string]SourceConfig, len(cfg.Sources))
	for sourceName, sourceCfg := range cfg.Sources {
		normalizedSourceName := strings.ToLower(strings.TrimSpace(sourceName))
		if normalizedSourceName == "" {
			return fmt.Errorf("sources contains an empty source name")
		}
		normalizedHeaders := make(map[string]string, len(sourceCfg.Headers))
		for headerName, headerValue := range sourceCfg.Headers {
			normalizedHeaderName := http.CanonicalHeaderKey(strings.TrimSpace(headerName))
			if normalizedHeaderName == "" {
				return fmt.Errorf("sources.%s.headers contains an empty header name", normalizedSourceName)
			}
			normalizedHeaders[normalizedHeaderName] = strings.TrimSpace(headerValue)
		}
		sourceCfg.Headers = normalizedHeaders
		normalizedSourceCfg[normalizedSourceName] = sourceCfg
	}
	cfg.Sources = normalizedSourceCfg

	return nil
}
