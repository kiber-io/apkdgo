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

- `--file`, `-f`:
  Provide a file containing a list of package names. Example:
  ```bash
  apkd -f packages.txt
  ```

- `--dev`:
  Enable batch download mode for all apps from a specific developer. Example:
  ```bash
  apkd --dev developer_name
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

## License

This project is licensed under the MIT License.