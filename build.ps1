param(
    [switch]$all
)

$OUTPUT_DIR = "./build"

if (Test-Path -Path $OUTPUT_DIR) {
    Remove-Item -Recurse -Force -Path $OUTPUT_DIR
}
New-Item -ItemType Directory -Path $OUTPUT_DIR | Out-Null

$version = Get-Content -Path "VERSION" -Raw
$commit = git rev-parse --short HEAD
$buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

function Build {
    param (
        [string]$os,
        [string]$arch,
        [string]$output_name
    )

    Write-Output "Building for $os $arch..."

    & cmd /c "set GOOS=$os&& set GOARCH=$arch&& set CGO_ENABLED=0&& go build -o `"$OUTPUT_DIR\$output_name`" -ldflags `"-s -w -X 'main.version=$version' -X 'main.commit=$commit' -X 'main.buildDate=$buildDate'`" -trimpath ./apkd/"

    if ($?) {
        Write-Output "Successfully built $output_name"
    } else {
        Write-Output "Failed to build $output_name"
    }
}

if ($all) {
    Build "windows" "amd64" "apkd-$version-windows-amd64.exe"
    Build "windows" "386" "apkd-$version-windows-386.exe"
    Build "linux" "amd64" "apkd-$version-linux-amd64"
    Build "linux" "arm64" "apkd-$version-linux-arm64"
    Build "linux" "386" "apkd-$version-linux-386"
    Build "darwin" "amd64" "apkd-$version-darwin-amd64"
    Build "darwin" "arm64" "apkd-$version-darwin-arm64"
} else {
    $currentArch = $env:GOARCH
    if (-not $currentArch) {
        switch ($env:PROCESSOR_ARCHITECTURE) {
            "AMD64" { $currentArch = "amd64" }
            "x86"   { $currentArch = "386" }
            "ARM64" { $currentArch = "arm64" }
            default { $currentArch = $env:PROCESSOR_ARCHITECTURE }
        }
    }
    Build "windows" $currentArch "apkd-$version-windows-$currentArch.exe"
}

Write-Output "Build process completed."
