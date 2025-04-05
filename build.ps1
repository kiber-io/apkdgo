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

    $env:GOOS = $os
    $env:GOARCH = $arch
    & go build -o "$OUTPUT_DIR\$output_name" -ldflags "-X 'main.version=$version' -X 'main.commit=$commit' -X 'main.buildDate=$buildDate'" ./apkd/

    if ($?) {
        Write-Output "Successfully built $output_name"
    } else {
        Write-Output "Failed to build $output_name"
    }
}

Build "windows" "amd64" "apkd-$version-windows-amd64.exe"
Build "linux" "amd64" "apkd-$version-linux-amd64"

Write-Output "Build process completed."