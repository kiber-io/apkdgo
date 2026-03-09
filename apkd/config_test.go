package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiber-io/apkd/apkd/sources"
	"github.com/spf13/cobra"
)

type mainStateSnapshot struct {
	configFile              string
	forceDownload           bool
	batchDeveloperMode      bool
	outputDir               string
	outputFileName          string
	globalProxy             string
	proxyInsecureSkipVerify bool
	sourceProxyEntries      []string
	verbosity               int
	selectedSources         []string
	workers                 int
}

func snapshotMainState() mainStateSnapshot {
	return mainStateSnapshot{
		configFile:              configFile,
		forceDownload:           forceDownload,
		batchDeveloperMode:      batchDeveloperDownloadMode,
		outputDir:               outputDir,
		outputFileName:          outputFileName,
		globalProxy:             globalProxy,
		proxyInsecureSkipVerify: proxyInsecureSkipVerify,
		sourceProxyEntries:      append([]string(nil), sourceProxyEntries...),
		verbosity:               verbosity,
		selectedSources:         append([]string(nil), selectedSources...),
		workers:                 workers,
	}
}

func restoreMainState(state mainStateSnapshot) {
	configFile = state.configFile
	forceDownload = state.forceDownload
	batchDeveloperDownloadMode = state.batchDeveloperMode
	outputDir = state.outputDir
	outputFileName = state.outputFileName
	globalProxy = state.globalProxy
	proxyInsecureSkipVerify = state.proxyInsecureSkipVerify
	sourceProxyEntries = append([]string(nil), state.sourceProxyEntries...)
	verbosity = state.verbosity
	selectedSources = append([]string(nil), state.selectedSources...)
	workers = state.workers
}

func newConfigApplyCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Count("verbose", "")
	cmd.Flags().Bool("force", false, "")
	cmd.Flags().Bool("dev", false, "")
	cmd.Flags().String("output-dir", "", "")
	cmd.Flags().String("output-file", "", "")
	cmd.Flags().StringArray("source", nil, "")
	cmd.Flags().Int("workers", *builtInDefaultConfig.Runtime.Workers, "")
	cmd.Flags().String("proxy", "", "")
	cmd.Flags().Bool("proxy-insecure", false, "")
	cmd.Flags().StringArray("source-proxy", nil, "")
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("failed to parse test flags: %v", err)
	}
	return cmd
}

func writeTestConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	return path
}

func TestLoadConfigUnknownKey(t *testing.T) {
	configPath := writeTestConfig(t, `
version: 1
defaults:
  verbose: 1
unknown_key: true
`)
	if _, err := loadConfig(configPath); err == nil {
		t.Fatalf("expected error for unknown config key")
	}
}

func TestLoadConfigUsesBuiltInDefaultsWhenPathIsEmpty(t *testing.T) {
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatalf("unexpected load config error: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected built-in default config, got nil")
	}
	if cfg.Version != defaultConfigVersion {
		t.Fatalf("unexpected default config version: %d", cfg.Version)
	}
	if cfg.Defaults.Verbose == nil || *cfg.Defaults.Verbose != *builtInDefaultConfig.Defaults.Verbose {
		t.Fatalf("unexpected default verbosity: %v", cfg.Defaults.Verbose)
	}
	if cfg.Runtime.Workers == nil || *cfg.Runtime.Workers != *builtInDefaultConfig.Runtime.Workers {
		t.Fatalf("unexpected default workers: %v", cfg.Runtime.Workers)
	}
}

func TestLoadConfigUsesBuiltInDefaultsWhenFileIsEmpty(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write empty config file: %v", err)
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected load config error: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected built-in default config, got nil")
	}
	if cfg.Defaults.Verbose == nil || *cfg.Defaults.Verbose != *builtInDefaultConfig.Defaults.Verbose {
		t.Fatalf("unexpected default verbosity from empty config file: %v", cfg.Defaults.Verbose)
	}
	if cfg.Runtime.Workers == nil || *cfg.Runtime.Workers != *builtInDefaultConfig.Runtime.Workers {
		t.Fatalf("unexpected default workers from empty config file: %v", cfg.Runtime.Workers)
	}
}

func TestLoadConfigResolvesOutputDirRelativeToConfigPath(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "apkd.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\ndefaults:\n  output_dir: ./downloads\n"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("failed to change working directory: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected load config error: %v", err)
	}
	if cfg.Defaults.OutputDir == nil {
		t.Fatalf("expected output_dir to be set")
	}
	expectedOutputDir := filepath.Join(configDir, "downloads")
	if *cfg.Defaults.OutputDir != expectedOutputDir {
		t.Fatalf("unexpected output_dir: got=%q expected=%q", *cfg.Defaults.OutputDir, expectedOutputDir)
	}
}

func TestResolveConfigPathExplicit(t *testing.T) {
	configPath := writeTestConfig(t, "version: 1\n")
	got, err := resolveConfigPath(configPath)
	if err != nil {
		t.Fatalf("unexpected resolve config path error: %v", err)
	}
	expected, err := filepath.Abs(configPath)
	if err != nil {
		t.Fatalf("unexpected abs path error: %v", err)
	}
	if got != expected {
		t.Fatalf("unexpected resolved path: got=%q expected=%q", got, expected)
	}
}

func TestResolveConfigPathDefault(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("APPDATA", configRoot)
	defaultPath := filepath.Join(configRoot, "apkd", "config.yml")
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0755); err != nil {
		t.Fatalf("failed to create default config directory: %v", err)
	}
	if err := os.WriteFile(defaultPath, []byte("version: 1\n"), 0644); err != nil {
		t.Fatalf("failed to write default config file: %v", err)
	}

	got, err := resolveConfigPath("")
	if err != nil {
		t.Fatalf("unexpected resolve config path error: %v", err)
	}
	if got != defaultPath {
		t.Fatalf("unexpected resolved default path: got=%q expected=%q", got, defaultPath)
	}
}

func TestResolveConfigPathDefaultMissing(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("APPDATA", configRoot)

	got, err := resolveConfigPath("")
	if err != nil {
		t.Fatalf("unexpected resolve config path error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty resolved path when default config is missing, got %q", got)
	}
}

func TestApplyConfigUsesDefaultConfigPath(t *testing.T) {
	state := snapshotMainState()
	t.Cleanup(func() {
		restoreMainState(state)
	})

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("APPDATA", configRoot)
	defaultPath := filepath.Join(configRoot, "apkd", "config.yml")
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0755); err != nil {
		t.Fatalf("failed to create default config directory: %v", err)
	}
	if err := os.WriteFile(defaultPath, []byte("version: 1\ndefaults:\n  force: true\n"), 0644); err != nil {
		t.Fatalf("failed to write default config file: %v", err)
	}

	configFile = ""
	forceDownload = false

	resolvedCfg, _, err := applyConfig(newConfigApplyCommand(t))
	if err != nil {
		t.Fatalf("unexpected apply config error: %v", err)
	}
	if resolvedCfg.path != defaultPath {
		t.Fatalf("unexpected resolved config path: got=%q expected=%q", resolvedCfg.path, defaultPath)
	}
	if !forceDownload {
		t.Fatalf("expected defaults.force from default config path to be applied")
	}
}

func TestApplyConfigWithoutConfigFileUsesBuiltInDefaults(t *testing.T) {
	state := snapshotMainState()
	t.Cleanup(func() {
		restoreMainState(state)
	})

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("APPDATA", configRoot)

	configFile = ""
	verbosity = 0
	workers = 0

	resolvedCfg, overrideLogs, err := applyConfig(newConfigApplyCommand(t))
	if err != nil {
		t.Fatalf("unexpected apply config error: %v", err)
	}
	if resolvedCfg.path != "" {
		t.Fatalf("expected empty resolved path when no config file exists, got %q", resolvedCfg.path)
	}
	if len(overrideLogs) != 0 {
		t.Fatalf("expected no override logs without config file, got %v", overrideLogs)
	}
	if verbosity != *builtInDefaultConfig.Defaults.Verbose {
		t.Fatalf("unexpected built-in default verbosity: %d", verbosity)
	}
	if workers != *builtInDefaultConfig.Runtime.Workers {
		t.Fatalf("unexpected built-in default workers: %d", workers)
	}
}

func TestApplyConfigUsesConfigValues(t *testing.T) {
	state := snapshotMainState()
	t.Cleanup(func() {
		restoreMainState(state)
	})
	configFile = writeTestConfig(t, `
version: 1
defaults:
  verbose: 2
  force: true
  dev: true
  output_dir: ./downloads
  sources:
    - FDROID
runtime:
  workers: 5
network:
  timeout: 45s
  retry:
    max_attempts: 7
    delay_ms: 1500
    max_delay_ms: 9000
    retry_status: [429, 500]
  proxy:
    global: http://127.0.0.1:8080
    insecure_skip_verify: true
    per_source:
      RuStore: http://127.0.0.1:8081
sources:
  rustore:
    profile:
      app_version: "1.95.0.1"
      app_version_code: "1095001"
      firmware_lang: en
    headers:
      user-agent: custom-agent
`)
	verbosity = 0
	forceDownload = false
	batchDeveloperDownloadMode = false
	outputDir = ""
	outputFileName = ""
	globalProxy = ""
	proxyInsecureSkipVerify = false
	sourceProxyEntries = nil
	selectedSources = nil
	workers = *builtInDefaultConfig.Runtime.Workers

	resolvedCfg, overrideLogs, err := applyConfig(newConfigApplyCommand(t))
	if err != nil {
		t.Fatalf("unexpected apply config error: %v", err)
	}
	if len(overrideLogs) != 0 {
		t.Fatalf("expected no override logs, got %v", overrideLogs)
	}
	if verbosity != 2 {
		t.Fatalf("expected verbosity=2, got %d", verbosity)
	}
	if !forceDownload {
		t.Fatalf("expected force=true from config")
	}
	if !batchDeveloperDownloadMode {
		t.Fatalf("expected dev=true from config")
	}
	if !filepath.IsAbs(outputDir) || filepath.Base(outputDir) != "downloads" {
		t.Fatalf("expected absolute outputDir ending with downloads, got %q", outputDir)
	}
	if outputFileName != "" {
		t.Fatalf("expected outputFileName to remain empty when not set via CLI, got %q", outputFileName)
	}
	if globalProxy != "http://127.0.0.1:8080" {
		t.Fatalf("expected globalProxy from config, got %q", globalProxy)
	}
	if !proxyInsecureSkipVerify {
		t.Fatalf("expected proxyInsecureSkipVerify=true from config")
	}
	if workers != 5 {
		t.Fatalf("expected workers=5 from config, got %d", workers)
	}
	if len(selectedSources) != 1 || selectedSources[0] != "fdroid" {
		t.Fatalf("expected selectedSources [fdroid], got %v", selectedSources)
	}
	if len(sourceProxyEntries) != 1 || sourceProxyEntries[0] != "rustore=http://127.0.0.1:8081" {
		t.Fatalf("unexpected sourceProxyEntries: %v", sourceProxyEntries)
	}
	if resolvedCfg.clientTimeout == nil || resolvedCfg.clientTimeout.String() != "45s" {
		t.Fatalf("unexpected client timeout: %v", resolvedCfg.clientTimeout)
	}
	if resolvedCfg.retryPolicy == nil || resolvedCfg.retryPolicy.MaxAttempts != 7 {
		t.Fatalf("unexpected retry policy: %+v", resolvedCfg.retryPolicy)
	}
	if resolvedCfg.sourceHeaders["rustore"]["User-Agent"] != "custom-agent" {
		t.Fatalf("expected User-Agent source override, got %v", resolvedCfg.sourceHeaders)
	}
	profileAny, profileExists := resolvedCfg.sourceProfiles["rustore"]
	if !profileExists {
		t.Fatalf("expected rustore source profile to be decoded")
	}
	profile, ok := profileAny.(sources.RuStoreProfile)
	if !ok {
		t.Fatalf("expected rustore profile type, got %T", profileAny)
	}
	if profile.AppVersion != "1.95.0.1" || profile.AppVersionCode != "1095001" || profile.FirmwareLang != "en" {
		t.Fatalf("unexpected rustore profile values: %+v", profile)
	}
	if _, ok := resolvedCfg.configuredSourceNames["rustore"]; !ok {
		t.Fatalf("expected configuredSourceNames to include rustore")
	}
}

func TestApplyConfigRejectsUnknownProfileField(t *testing.T) {
	state := snapshotMainState()
	t.Cleanup(func() {
		restoreMainState(state)
	})
	configFile = writeTestConfig(t, `
version: 1
sources:
  rustore:
    profile:
      unknown_key: value
`)

	if _, _, err := applyConfig(newConfigApplyCommand(t)); err == nil {
		t.Fatalf("expected error for unknown rustore profile key")
	} else if !strings.Contains(err.Error(), "sources.rustore.profile") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestApplyConfigRejectsInvalidProfileValue(t *testing.T) {
	state := snapshotMainState()
	t.Cleanup(func() {
		restoreMainState(state)
	})
	configFile = writeTestConfig(t, `
version: 1
sources:
  rustore:
    profile:
      app_version_code: "abc"
`)

	if _, _, err := applyConfig(newConfigApplyCommand(t)); err == nil {
		t.Fatalf("expected error for invalid rustore profile value")
	} else if !strings.Contains(err.Error(), "app_version_code") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestApplyConfigCliOverrideLogs(t *testing.T) {
	state := snapshotMainState()
	t.Cleanup(func() {
		restoreMainState(state)
	})
	configFile = writeTestConfig(t, `
version: 1
defaults:
  force: true
  sources: [fdroid]
runtime:
  workers: 2
network:
  proxy:
    global: http://127.0.0.1:8080
    per_source:
      rustore: http://127.0.0.1:8081
`)

	forceDownload = false
	selectedSources = []string{"apkcombo"}
	workers = 7
	globalProxy = "http://127.0.0.1:9000"
	sourceProxyEntries = []string{"fdroid=http://127.0.0.1:9999"}
	cmd := newConfigApplyCommand(
		t,
		"--force=false",
		"--source=apkcombo",
		"--workers=7",
		"--proxy=http://127.0.0.1:9000",
		"--source-proxy=fdroid=http://127.0.0.1:9999",
	)

	_, overrideLogs, err := applyConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected apply config error: %v", err)
	}
	if forceDownload {
		t.Fatalf("expected CLI force value to remain false")
	}
	if len(selectedSources) != 1 || selectedSources[0] != "apkcombo" {
		t.Fatalf("expected CLI selectedSources to remain unchanged, got %v", selectedSources)
	}
	if workers != 7 {
		t.Fatalf("expected CLI workers to remain 7, got %d", workers)
	}
	if globalProxy != "http://127.0.0.1:9000" {
		t.Fatalf("expected CLI proxy to remain unchanged, got %q", globalProxy)
	}
	if len(sourceProxyEntries) != 1 || sourceProxyEntries[0] != "fdroid=http://127.0.0.1:9999" {
		t.Fatalf("expected CLI sourceProxyEntries to remain unchanged, got %v", sourceProxyEntries)
	}

	joinedLogs := strings.Join(overrideLogs, "\n")
	for _, expected := range []string{
		"CLI flag --force overrides config value defaults.force",
		"CLI flag --source overrides config value defaults.sources",
		"CLI flag --workers overrides config value runtime.workers",
		"CLI flag --proxy overrides config value network.proxy.global",
		"CLI flag --source-proxy overrides config value network.proxy.per_source",
	} {
		if !strings.Contains(joinedLogs, expected) {
			t.Fatalf("expected override log %q in %q", expected, joinedLogs)
		}
	}
}
