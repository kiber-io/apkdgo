package main

import (
	"fmt"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kiber-io/apkd/apkd/logging"
	"github.com/kiber-io/apkd/apkd/network"
	"github.com/kiber-io/apkd/apkd/sources"

	"github.com/spf13/cobra"
)

var packageNamesMap = make(map[string]int)
var forceDownload bool
var batchDeveloperDownloadMode bool
var outputDir string
var outputFileName string
var globalProxy string
var proxyInsecureSkipVerify bool
var sourceProxyEntries []string
var configFile string
var packagesFile string
var packageNames []string
var verbosity int
var listSources bool
var printVersion bool
var workers int

var selectedSources []string
var activeSources []sources.Source

var downloadSuccessCount atomic.Int64
var downloadErrorCount atomic.Int64

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

var rootCmd = cobra.Command{
	Use:   "apkd",
	Short: "apkd is a tool for downloading APKs from multiple sources",
	PreRun: func(cmd *cobra.Command, args []string) {
		// Reset mutable global state to keep repeated in-process runs deterministic.
		packageNamesMap = make(map[string]int)
		activeSources = nil
		if printVersion {
			return
		}
		resolvedCfg, configOverrideLogs, err := applyConfig(cmd)
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			os.Exit(1)
		}
		if workers <= 0 {
			fmt.Println("Error validating workers: --workers must be > 0")
			os.Exit(1)
		}
		if verbosity == 0 {
			verbosity = *builtInDefaultConfig.Defaults.Verbose
		}
		logging.Init(verbosity)
		if resolvedCfg.path != "" {
			logging.Logi(fmt.Sprintf("Loaded config: %s", resolvedCfg.path))
		}
		for _, overrideLog := range configOverrideLogs {
			logging.Logd(overrideLog)
		}
		if err := network.ConfigureSourceHeaderOverrides(resolvedCfg.sourceHeaders); err != nil {
			fmt.Printf("Error applying source header settings: %v\n", err)
			os.Exit(1)
		}
		network.ResetClientDefaults()
		if err := network.ConfigureClientDefaults(resolvedCfg.clientTimeout, resolvedCfg.retryPolicy); err != nil {
			fmt.Printf("Error applying network settings: %v\n", err)
			os.Exit(1)
		}

		sourceProxies, err := parseSourceProxyEntries(sourceProxyEntries)
		if err != nil {
			fmt.Printf("Error parsing --source-proxy: %v\n", err)
			os.Exit(1)
		}
		if err := network.ConfigureProxies(globalProxy, sourceProxies, proxyInsecureSkipVerify); err != nil {
			fmt.Printf("Error applying proxy settings: %v\n", err)
			os.Exit(1)
		}
		sources.ConfigureSourceProfiles(resolvedCfg.sourceProfiles)

		if err := sources.InitializeRegisteredSources(); err != nil {
			fmt.Printf("Error initializing sources: %v\n", err)
			os.Exit(1)
		}

		if listSources {
			return
		}

		if packagesFile != "" {
			file, err := os.Open(packagesFile)
			if err != nil {
				fmt.Printf("Error opening file %s: %v\n", packagesFile, err)
				os.Exit(1)
			}
			defer file.Close()

			var packageName string
			for {
				_, err := fmt.Fscanf(file, "%s\n", &packageName)
				if err != nil {
					break
				}
				// support comments
				if strings.HasPrefix(packageName, "#") {
					continue
				}
				packageNames = append(packageNames, packageName)
			}
		}

		for _, pkgName := range packageNames {
			var versionCode int
			if strings.Contains(pkgName, ":") {
				parts := strings.Split(pkgName, ":")
				pkgName = parts[0]
				var err error
				versionCode, err = strconv.Atoi(parts[1])
				if err != nil {
					fmt.Printf("Error parsing version code for package %s: %v\n", pkgName, err)
					os.Exit(1)
				}
			}
			packageNamesMap[pkgName] = versionCode
		}

		if len(packageNamesMap) == 0 {
			fmt.Println("No package names provided. Use --package or --file to specify package names.")
			os.Exit(1)
		}

		for i, src := range selectedSources {
			selectedSources[i] = strings.ToLower(src)
		}
		allSources := sources.GetAll()
		if err := validateKnownSources(selectedSources, sourceProxies, resolvedCfg.configuredSourceNames, allSources); err != nil {
			fmt.Printf("Error validating source names: %v\n", err)
			os.Exit(1)
		}
		if len(selectedSources) > 0 {
			selectedSourcesSet := make(map[string]struct{}, len(selectedSources))
			for _, src := range selectedSources {
				selectedSourcesSet[src] = struct{}{}
			}
			for src := range allSources {
				if _, exists := selectedSourcesSet[src]; exists {
					activeSources = append(activeSources, allSources[src])
				}
			}
		} else {
			for src := range allSources {
				activeSources = append(activeSources, allSources[src])
			}
		}
		if len(activeSources) == 0 {
			fmt.Println("No sources available. Please check your sources.")
			os.Exit(1)
		}
		if outputDir != "" {
			outputDir, err, warn := sanitizedAndAbsoluteName(outputDir)
			if err != nil {
				fmt.Printf("Error getting absolute path for output directory %s: %v\n", outputDir, err)
				os.Exit(1)
			}
			if warn != nil {
				fmt.Println("Warning:", warn)
			}
			info, err := os.Stat(outputDir)
			if os.IsNotExist(err) {
				err = os.MkdirAll(outputDir, 0755)
				if err != nil {
					fmt.Printf("Error creating output directory %s: %v\n", outputDir, err)
					os.Exit(1)
				}
			} else if err != nil {
				fmt.Printf("Error checking output directory %s: %v\n", outputDir, err)
				os.Exit(1)
			} else if !info.IsDir() {
				fmt.Printf("Output path %s is not a directory\n", outputDir)
				os.Exit(1)
			}
		}
		if outputFileName != "" {
			if len(packageNamesMap) > 1 {
				fmt.Println("Output file name is not supported when downloading multiple packages.")
				os.Exit(1)
			}
			outputFileName, err, warn := sanitizedAndAbsoluteName(outputFileName)
			if err != nil {
				fmt.Printf("Error getting absolute path for output file %s: %v\n", outputFileName, err)
				os.Exit(1)
			}
			if warn != nil {
				fmt.Println("Warning:", warn)
			}
			if _, err := os.Stat(outputFileName); os.IsExist(err) {
				if !forceDownload {
					fmt.Printf("Output file %s already exists. Use --force to overwrite.\n", outputFileName)
					os.Exit(1)
				}
			}
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		if printVersion {
			fmt.Printf("Version: %s\nCommit: %s\nBuilt at: %s\n", version, commit, buildDate)
		} else if listSources {
			fmt.Println("Available sources:")
			allSources := sources.GetAll()
			for _, src := range allSources {
				fmt.Printf("- %s\n", src.Name())
			}
		} else {
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigChan
				printSummary()
				os.Exit(0)
			}()

			tq := NewTaskQueue(workers)
			for packageName, versionCode := range packageNamesMap {
				tq.AddTask(PackageTask{
					PackageName: packageName,
					VersionCode: versionCode,
				})
			}

			tq.Wait()
			printSummary()
		}
	},
}

func reportError(errText string) {
	downloadErrorCount.Add(1)
	logging.Loge(strings.ReplaceAll(errText, "\n", "\\n"))
}

func reportDownloadSuccess() {
	downloadSuccessCount.Add(1)
}

func printSummary() {
	fmt.Printf("\nSummary: downloaded %d, errors %d\n", downloadSuccessCount.Load(), downloadErrorCount.Load())
}

type resolvedConfig struct {
	path                  string
	sourceHeaders         map[string]map[string]string
	sourceProfiles        map[string]any
	configuredSourceNames map[string]struct{}
	clientTimeout         *time.Duration
	retryPolicy           *network.RetryPolice
}

func applyConfig(cmd *cobra.Command) (*resolvedConfig, []string, error) {
	resolved := &resolvedConfig{
		sourceHeaders:         make(map[string]map[string]string),
		sourceProfiles:        make(map[string]any),
		configuredSourceNames: make(map[string]struct{}),
	}
	configPath, err := resolveConfigPath(configFile)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil {
		return resolved, nil, nil
	}
	resolved.path = configPath
	var overrideLogs []string
	recordOverride := func(message string) {
		if resolved.path == "" {
			return
		}
		overrideLogs = append(overrideLogs, message)
	}

	if cfg.Defaults.Verbose != nil {
		if cmd.Flags().Changed("verbose") {
			recordOverride("CLI flag --verbose overrides config value defaults.verbose")
		} else {
			verbosity = *cfg.Defaults.Verbose
		}
	}
	if cfg.Defaults.Force != nil {
		if cmd.Flags().Changed("force") {
			recordOverride("CLI flag --force overrides config value defaults.force")
		} else {
			forceDownload = *cfg.Defaults.Force
		}
	}
	if cfg.Defaults.Dev != nil {
		if cmd.Flags().Changed("dev") {
			recordOverride("CLI flag --dev overrides config value defaults.dev")
		} else {
			batchDeveloperDownloadMode = *cfg.Defaults.Dev
		}
	}
	if cfg.Defaults.OutputDir != nil {
		if cmd.Flags().Changed("output-dir") {
			recordOverride("CLI flag --output-dir overrides config value defaults.output_dir")
		} else {
			outputDir = *cfg.Defaults.OutputDir
		}
	}
	if len(cfg.Defaults.Sources) > 0 {
		if cmd.Flags().Changed("source") {
			recordOverride("CLI flag --source overrides config value defaults.sources")
		} else {
			selectedSources = append([]string(nil), cfg.Defaults.Sources...)
		}
	}
	if cfg.Runtime.Workers != nil {
		if cmd.Flags().Changed("workers") {
			recordOverride("CLI flag --workers overrides config value runtime.workers")
		} else {
			workers = *cfg.Runtime.Workers
		}
	}
	if cfg.Network.Proxy.Global != nil {
		if cmd.Flags().Changed("proxy") {
			recordOverride("CLI flag --proxy overrides config value network.proxy.global")
		} else {
			globalProxy = *cfg.Network.Proxy.Global
		}
	}
	if cfg.Network.Proxy.InsecureSkipVerify != nil {
		if cmd.Flags().Changed("proxy-insecure") {
			recordOverride("CLI flag --proxy-insecure overrides config value network.proxy.insecure_skip_verify")
		} else {
			proxyInsecureSkipVerify = *cfg.Network.Proxy.InsecureSkipVerify
		}
	}
	if len(cfg.Network.Proxy.PerSource) > 0 {
		if cmd.Flags().Changed("source-proxy") {
			recordOverride("CLI flag --source-proxy overrides config value network.proxy.per_source")
		} else {
			sourceProxyEntries = sourceProxyMapToEntries(cfg.Network.Proxy.PerSource)
		}
	}
	if cfg.Network.Timeout != nil {
		timeout := *cfg.Network.Timeout
		resolved.clientTimeout = &timeout
	}
	if cfg.Network.Retry.IsSet() {
		retryPolicy, err := buildRetryPolicy(cfg.Network.Retry)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid network.retry configuration: %w", err)
		}
		resolved.retryPolicy = retryPolicy
	}
	for sourceName, sourceCfg := range cfg.Sources {
		resolved.configuredSourceNames[sourceName] = struct{}{}
		if sourceCfg.Profile != nil && sourceCfg.Profile.Node != nil {
			profile, err := sources.DecodeSourceProfile(sourceName, sourceCfg.Profile.Node)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid sources.%s.profile: %w", sourceName, err)
			}
			if profile != nil {
				resolved.sourceProfiles[sourceName] = profile
			}
		}
		if len(sourceCfg.Headers) == 0 {
			continue
		}
		headers := make(map[string]string, len(sourceCfg.Headers))
		maps.Copy(headers, sourceCfg.Headers)
		resolved.sourceHeaders[sourceName] = headers
	}

	return resolved, overrideLogs, nil
}

func buildRetryPolicy(cfg ConfigRetry) (*network.RetryPolice, error) {
	retryPolicy := network.DefaultRetryPolice()
	if cfg.MaxAttempts != nil {
		retryPolicy.MaxAttempts = *cfg.MaxAttempts
	}
	if cfg.DelayMs != nil {
		retryPolicy.Delay = *cfg.DelayMs
	}
	if cfg.MaxDelayMs != nil {
		retryPolicy.MaxDelay = *cfg.MaxDelayMs
	}
	if len(cfg.RetryStatus) > 0 {
		retryPolicy.RetryStatus = append([]int(nil), cfg.RetryStatus...)
	}
	if retryPolicy.MaxAttempts <= 0 {
		return nil, fmt.Errorf("max_attempts must be > 0")
	}
	if retryPolicy.Delay < 0 {
		return nil, fmt.Errorf("delay_ms must be >= 0")
	}
	if retryPolicy.MaxDelay < 0 {
		return nil, fmt.Errorf("max_delay_ms must be >= 0")
	}
	for _, retryStatusCode := range retryPolicy.RetryStatus {
		if retryStatusCode < 100 || retryStatusCode > 599 {
			return nil, fmt.Errorf("retry_status contains invalid HTTP status code %d", retryStatusCode)
		}
	}
	return retryPolicy, nil
}

func sourceProxyMapToEntries(sourceProxies map[string]string) []string {
	sourceNames := make([]string, 0, len(sourceProxies))
	for sourceName := range sourceProxies {
		sourceNames = append(sourceNames, sourceName)
	}
	sort.Strings(sourceNames)
	entries := make([]string, 0, len(sourceNames))
	for _, sourceName := range sourceNames {
		entries = append(entries, fmt.Sprintf("%s=%s", sourceName, sourceProxies[sourceName]))
	}
	return entries
}

func parseSourceProxyEntries(entries []string) (map[string]string, error) {
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		normalizedEntry := strings.TrimSpace(entry)
		if normalizedEntry == "" {
			continue
		}
		parts := strings.SplitN(normalizedEntry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid value %q, expected source=proxy-url", entry)
		}
		sourceName := strings.ToLower(strings.TrimSpace(parts[0]))
		proxyURL := strings.TrimSpace(parts[1])
		if sourceName == "" || proxyURL == "" {
			return nil, fmt.Errorf("invalid value %q, source and proxy URL must be non-empty", entry)
		}
		result[sourceName] = proxyURL
	}
	return result, nil
}

func validateKnownSources(
	selectedSources []string,
	sourceProxies map[string]string,
	configuredSourceNames map[string]struct{},
	allSources map[string]sources.Source,
) error {
	knownSources := make(map[string]struct{}, len(allSources))
	for sourceName := range allSources {
		knownSources[sourceName] = struct{}{}
	}
	unknownSelected := make(map[string]struct{})
	for _, sourceName := range selectedSources {
		if _, exists := knownSources[sourceName]; !exists {
			unknownSelected[sourceName] = struct{}{}
		}
	}
	unknownSourceProxies := make(map[string]struct{})
	for sourceName := range sourceProxies {
		if _, exists := knownSources[sourceName]; !exists {
			unknownSourceProxies[sourceName] = struct{}{}
		}
	}
	unknownConfiguredSources := make(map[string]struct{})
	for sourceName := range configuredSourceNames {
		if _, exists := knownSources[sourceName]; !exists {
			unknownConfiguredSources[sourceName] = struct{}{}
		}
	}
	if len(unknownSelected) == 0 && len(unknownSourceProxies) == 0 && len(unknownConfiguredSources) == 0 {
		return nil
	}
	parts := make([]string, 0, 3)
	if len(unknownSelected) > 0 {
		parts = append(parts, fmt.Sprintf("--source: %s", joinSortedSet(unknownSelected)))
	}
	if len(unknownSourceProxies) > 0 {
		parts = append(parts, fmt.Sprintf("--source-proxy: %s", joinSortedSet(unknownSourceProxies)))
	}
	if len(unknownConfiguredSources) > 0 {
		parts = append(parts, fmt.Sprintf("config.sources: %s", joinSortedSet(unknownConfiguredSources)))
	}
	return fmt.Errorf("unknown source name(s) in %s. Use --list-sources to see available sources", strings.Join(parts, "; "))
}

func joinSortedSet(values map[string]struct{}) string {
	sortedValues := make([]string, 0, len(values))
	for value := range values {
		sortedValues = append(sortedValues, value)
	}
	sort.Strings(sortedValues)
	return strings.Join(sortedValues, ", ")
}

func main() {
	rootCmd.PersistentFlags().StringArrayVarP(&packageNames, "package", "p", []string{}, "package name of the app")
	rootCmd.PersistentFlags().StringArrayVarP(&selectedSources, "source", "s", []string{}, "specify source(s) for downloading")
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "path to YAML config file (defaults to ~/.config/apkd/config.yml if present)")
	rootCmd.PersistentFlags().StringVar(&globalProxy, "proxy", valueOrZero(builtInDefaultConfig.Network.Proxy.Global), "global proxy URL for all traffic")
	rootCmd.PersistentFlags().BoolVar(&proxyInsecureSkipVerify, "proxy-insecure", valueOrZero(builtInDefaultConfig.Network.Proxy.InsecureSkipVerify), "skip TLS certificate verification for requests sent through proxy")
	rootCmd.PersistentFlags().StringArrayVar(&sourceProxyEntries, "source-proxy", []string{}, "source proxy mapping in format source=proxy-url (can be repeated)")
	rootCmd.PersistentFlags().IntVar(&workers, "workers", *builtInDefaultConfig.Runtime.Workers, "number of worker goroutines")
	rootCmd.PersistentFlags().StringVarP(&packagesFile, "file", "f", "", "file containing package names")
	rootCmd.PersistentFlags().BoolVarP(&batchDeveloperDownloadMode, "dev", "", valueOrZero(builtInDefaultConfig.Defaults.Dev), "download all apps from developer")
	rootCmd.PersistentFlags().BoolVarP(&forceDownload, "force", "F", valueOrZero(builtInDefaultConfig.Defaults.Force), "force download even if the file already exists")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output-dir", "O", valueOrZero(builtInDefaultConfig.Defaults.OutputDir), "output directory for downloaded APKs")
	rootCmd.PersistentFlags().StringVarP(&outputFileName, "output-file", "o", "", "output file name for downloaded APKs")
	rootCmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "Set verbosity level. Use -v or -vv for more verbosity")
	rootCmd.PersistentFlags().BoolVarP(&listSources, "list-sources", "l", false, "list available sources")
	rootCmd.PersistentFlags().BoolVarP(&printVersion, "version", "V", false, "print version and exit")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func sanitizeFileName(name string) string {
	reg := regexp.MustCompile(`[<>:"/\\|?*]+`)
	safe := reg.ReplaceAllString(name, "-")
	safe = strings.TrimSpace(safe)
	if len(safe) > 255 {
		safe = safe[:255]
	}

	return safe
}

func sanitizedAndAbsoluteName(name string) (string, error, error) {
	absPath, err := filepath.Abs(name)
	if err != nil {
		return "", err, nil
	}
	base := filepath.Base(absPath)
	sanitizedName := sanitizeFileName(base)
	absPath = filepath.Join(filepath.Dir(absPath), sanitizedName)
	if base != sanitizedName {
		return absPath, nil, fmt.Errorf("name %s is not valid. Using %s instead", base, sanitizedName)
	}
	return absPath, nil, nil
}
