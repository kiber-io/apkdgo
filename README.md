# APK Downloader CLI

This project is a command-line tool for downloading APK files from various sources.

## Supported Sources:
- ApkCombo
- F-Droid
- RuStore
- Nashstore (may not work for non-Russian IP addresses)
- ApkPure (WIP)

## Usage

```bash
apkd [flags]
```

### Flags

- `--package`, `-p`:
  Specify the package name(s) of the app(s) to download. Example:
  ```bash
  apkd -p com.example.app
  ```

- `--source`, `-s`:
  Specify the source(s) for downloading APKs. Example:
  ```bash
  apkd -s apkpure -p com.example.app
  ```

- `--config`:
  Path to YAML config file. CLI flags override config values.
  If not specified, apkd tries `~/.config/apkd/config.yml`. Example:
  ```bash
  apkd --config ./apkd.yaml -p com.example.app
  ```

- `--file`, `-f`:
  Provide a file containing a list of package names. Example:
  ```bash
  apkd -f packages.txt
  ```

- `--dev`:
  Enable batch download mode for all apps from a specific developer. You need to specify the application package from the developer whose apps should be searched and downloaded using the `-p/--package` flag. Example:
  ```bash
  apkd --dev --package com.example.app
  ```

- `--force`, `-F`:
  Force download even if the file already exists. Example:
  ```bash
  apkd -F -p com.example.app
  ```

- `--output-dir`, `-O`:
  Specify the output directory for downloaded APKs. Example:
  ```bash
  apkd -O ./downloads -p com.example.app
  ```

- `--output-file`, `-o`:
  Specify the output file name for downloaded APKs. Example:
  ```bash
  apkd -o app.apk -p com.example.app
  ```

- `--proxy`:
  Set a global proxy URL for all network traffic. Example:
  ```bash
  apkd --proxy http://127.0.0.1:8080 -p com.example.app
  ```

- `--source-proxy`:
  Set proxy URL for a specific source in format `source=proxy-url` (can be repeated). Example:
  ```bash
  apkd --source-proxy rustore=http://127.0.0.1:8081 --source-proxy fdroid=http://127.0.0.1:8082 -p com.example.app
  ```

- `--workers`:
  Number of worker goroutines for task processing. Must be greater than 0. Example:
  ```bash
  apkd --workers 5 -p com.example.app
  ```

- `--proxy-insecure`:
  Skip TLS certificate verification for HTTPS requests sent through proxy.
  Useful for debugging with intercepting proxies. Example:
  ```bash
  apkd --proxy http://127.0.0.1:8080 --proxy-insecure -p com.example.app
  ```

- `--verbose`, `-v`:
  Set verbosity level. Use `-v` or `-vv` for more detailed logs. Example:
  ```bash
  apkd -v -p com.example.app
  ```

- `--version`, `-V`:
  Print the version information and exit. Example:
  ```bash
  apkd -V
  ```

- `--list-sources`, `-l`:
  List all available sources. Example:
  ```bash
  apkd -l
  ```

## Example

Download an APK for a specific package from a specific source:
```bash
apkd -p com.example.app -s apkpure -O ./downloads
```

Use global debug proxy:
```bash
apkd --proxy http://127.0.0.1:8080 -p org.fdroid.fdroid
```

Use source-specific proxy overrides:
```bash
apkd --proxy http://127.0.0.1:8080 --source-proxy rustore=http://127.0.0.1:8081 -p ru.vk.store
```

Use intercepting proxy with disabled TLS verification:
```bash
apkd --proxy http://127.0.0.1:8080 --proxy-insecure -p org.fdroid.fdroid
```

Use config defaults and override one value from CLI:
```bash
apkd --config ./apkd.yaml --proxy http://127.0.0.1:8080 -p org.fdroid.fdroid
```

## Configuration

Config format is YAML (`version: 1`). Precedence is:

1. CLI flags
2. Config values
3. Built-in code defaults

When a CLI flag overrides a config value, the tool logs it.

Default config lookup path (when `--config` is omitted):
- `~/.config/apkd/config.yml`

Example `apkd.yaml`:

```yaml
version: 1

defaults:
  sources: [rustore, fdroid]
  output_dir: ./downloads
  force: false
  verbose: 1

runtime:
  workers: 3

network:
  timeout: 30s
  retry:
    max_attempts: 10
    delay_ms: 1000
    max_delay_ms: 10000
    retry_status: [429, 500, 502, 503, 504]
  proxy:
    global: http://127.0.0.1:8080
    insecure_skip_verify: false
    per_source:
      rustore: http://127.0.0.1:8081

sources:
  rustore:
    profile:
      app_version: "1.93.0.3"
      app_version_code: "1093003"
    headers:
      User-Agent: "RuStore/1.93.0.3 (Android 14; SDK 34; arm64-v8a; Pixel 7; ru)"
      ruStoreVerCode: "1093003"
```

## License

This project is licensed under the MIT License.
