package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	defaultConfigVersion   = 2
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
	Node *yaml.Node
}

func (c *SourceConfig) UnmarshalYAML(value *yaml.Node) error {
	c.Node = cloneYAMLNode(value)
	return nil
}

type RawYAMLNode struct {
	Node *yaml.Node
}

func (n *RawYAMLNode) UnmarshalYAML(value *yaml.Node) error {
	n.Node = cloneYAMLNode(value)
	return nil
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	cloned := *node
	if len(node.Content) > 0 {
		cloned.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			cloned.Content[i] = cloneYAMLNode(child)
		}
	}
	if node.Alias != nil {
		cloned.Alias = cloneYAMLNode(node.Alias)
	}
	return &cloned
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
	normalizedPath := strings.TrimSpace(path)
	configDir := ""
	if normalizedPath != "" {
		configDir = filepath.Dir(normalizedPath)
	}
	if normalizedPath == "" {
		cfg := cloneBuiltInDefaultConfig()
		if err := normalizeConfig(cfg, configDir); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	file, err := os.Open(normalizedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file %s: %w", normalizedPath, err)
	}
	defer file.Close()

	cfg := cloneBuiltInDefaultConfig()
	configBytes, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", normalizedPath, err)
	}
	if strings.TrimSpace(string(configBytes)) == "" {
		if err := normalizeConfig(cfg, configDir); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err := ensureSingleYAMLDocument(configBytes); err != nil {
		return nil, err
	}
	if err := decodeYAMLBytesStrict(configBytes, cfg); err != nil {
		return nil, err
	}
	if err := normalizeConfig(cfg, configDir); err != nil {
		return nil, err
	}
	return cfg, nil
}

func normalizeConfig(cfg *AppConfig, configDir string) error {
	if cfg.Version == 0 {
		cfg.Version = defaultConfigVersion
	}

	if cfg.Defaults.Verbose != nil && *cfg.Defaults.Verbose < 0 {
		return errors.New("defaults.verbose must be >= 0")
	}
	if cfg.Runtime.Workers != nil && *cfg.Runtime.Workers <= 0 {
		return errors.New("runtime.workers must be > 0")
	}
	if cfg.Network.Timeout != nil && *cfg.Network.Timeout <= 0 {
		return errors.New("network.timeout must be > 0")
	}

	if cfg.Defaults.OutputDir != nil {
		outputDir := strings.TrimSpace(*cfg.Defaults.OutputDir)
		if !filepath.IsAbs(outputDir) && configDir != "" {
			outputDir = filepath.Join(configDir, outputDir)
		}
		outputDir, err := filepath.Abs(outputDir)
		if err != nil {
			return fmt.Errorf("error getting absolute path for output directory %s: %w", outputDir, err)
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
			return errors.New("defaults.sources contains an empty source name")
		}
		normalizedSelectedSources = append(normalizedSelectedSources, normalizedSourceName)
	}
	cfg.Defaults.Sources = normalizedSelectedSources

	normalizedProxyBySource := make(map[string]string, len(cfg.Network.Proxy.PerSource))
	for sourceName, rawProxyURL := range cfg.Network.Proxy.PerSource {
		normalizedSourceName := strings.ToLower(strings.TrimSpace(sourceName))
		normalizedProxyURL := strings.TrimSpace(rawProxyURL)
		if normalizedSourceName == "" {
			return errors.New("network.proxy.per_source contains an empty source name")
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
			return errors.New("sources contains an empty source name")
		}
		normalizedSourceCfg[normalizedSourceName] = sourceCfg
	}
	cfg.Sources = normalizedSourceCfg

	return nil
}

func decodeYAMLBytesStrict(data []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("failed to decode config: %w", err)
	}
	return nil
}

func ensureSingleYAMLDocument(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var first any
	if err := decoder.Decode(&first); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("failed to decode config: %w", err)
	}
	var extraDoc any
	if err := decoder.Decode(&extraDoc); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not supported")
		}
		return fmt.Errorf("failed to check for multiple YAML documents: %w", err)
	}
	return nil
}
