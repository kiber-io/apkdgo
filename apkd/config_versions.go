package main

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const configVersion1 = 1

type configParser interface {
	Parse(data []byte) (*AppConfig, error)
}

var configParsers = map[int]configParser{
	configVersion1:       configV1Parser{},
	defaultConfigVersion: configV2Parser{},
}

func getConfigParser(version int) (configParser, error) {
	parser, ok := configParsers[version]
	if !ok {
		return nil, fmt.Errorf("unsupported config version %d", version)
	}
	return parser, nil
}

func detectConfigVersion(data []byte) (int, error) {
	var versionOnly struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &versionOnly); err != nil {
		return 0, fmt.Errorf("failed to decode config version: %w", err)
	}
	if versionOnly.Version == 0 {
		return defaultConfigVersion, nil
	}
	return versionOnly.Version, nil
}

type configV2Parser struct{}

func (configV2Parser) Parse(data []byte) (*AppConfig, error) {
	cfg := cloneBuiltInDefaultConfig()
	if err := decodeYAMLBytesStrict(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Version == 0 {
		cfg.Version = defaultConfigVersion
	}
	return cfg, nil
}

type sourceConfigV1 struct {
	Profile *RawYAMLNode      `yaml:"profile"`
	Headers map[string]string `yaml:"headers"`
}

type appConfigV1 struct {
	Version  int                       `yaml:"version"`
	Defaults ConfigDefaults            `yaml:"defaults"`
	Runtime  ConfigRuntime             `yaml:"runtime"`
	Network  ConfigNetwork             `yaml:"network"`
	Sources  map[string]sourceConfigV1 `yaml:"sources"`
}

type configV1Parser struct{}

func (configV1Parser) Parse(data []byte) (*AppConfig, error) {
	var cfgV1 appConfigV1
	if err := decodeYAMLBytesStrict(data, &cfgV1); err != nil {
		return nil, err
	}
	cfg := cloneBuiltInDefaultConfig()
	cfg.Version = configVersion1
	cfg.Defaults = cfgV1.Defaults
	cfg.Runtime = cfgV1.Runtime
	cfg.Network = cfgV1.Network
	cfg.Sources = make(map[string]SourceConfig, len(cfgV1.Sources))
	for sourceName, sourceCfg := range cfgV1.Sources {
		flatNode, err := flattenV1SourceConfigNode(sourceName, sourceCfg)
		if err != nil {
			return nil, fmt.Errorf("invalid sources.%s: %w", sourceName, err)
		}
		cfg.Sources[sourceName] = SourceConfig{Node: flatNode}
	}
	return cfg, nil
}

func flattenV1SourceConfigNode(sourceName string, cfgV1 sourceConfigV1) (*yaml.Node, error) {
	normalizedHeaders, err := normalizeSourceHeaders(sourceName, cfgV1.Headers)
	if err != nil {
		return nil, err
	}
	flatNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if cfgV1.Profile != nil && cfgV1.Profile.Node != nil {
		if cfgV1.Profile.Node.Kind != yaml.MappingNode {
			return nil, errors.New("profile must be a mapping")
		}
		for i := 0; i < len(cfgV1.Profile.Node.Content); i += 2 {
			flatNode.Content = append(flatNode.Content, cloneYAMLNode(cfgV1.Profile.Node.Content[i]), cloneYAMLNode(cfgV1.Profile.Node.Content[i+1]))
		}
	}
	if len(normalizedHeaders) > 0 {
		headersNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		headerNames := make([]string, 0, len(normalizedHeaders))
		for headerName := range normalizedHeaders {
			headerNames = append(headerNames, headerName)
		}
		sort.Strings(headerNames)
		for _, headerName := range headerNames {
			headersNode.Content = append(headersNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: headerName},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: normalizedHeaders[headerName]},
			)
		}
		flatNode.Content = append(flatNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "headers"},
			headersNode,
		)
	}
	return flatNode, nil
}

func normalizeSourceHeaders(sourceName string, headers map[string]string) (map[string]string, error) {
	normalizedHeaders := make(map[string]string, len(headers))
	for headerName, headerValue := range headers {
		normalizedHeaderName := http.CanonicalHeaderKey(strings.TrimSpace(headerName))
		if normalizedHeaderName == "" {
			return nil, fmt.Errorf("headers contains an empty header name for source %s", sourceName)
		}
		normalizedHeaders[normalizedHeaderName] = strings.TrimSpace(headerValue)
	}
	return normalizedHeaders, nil
}
