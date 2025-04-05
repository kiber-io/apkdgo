#!/bin/bash
set -e

OUTPUT_DIR="./build"

rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

VERSION=$(cat VERSION)
COMMIT=$(git rev-parse --short HEAD)
BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")  # UTC ISO 8601

build() {
    local os=$1
    local arch=$2
    local output_name=$3

    echo "Building for $os $arch..."

    GOOS=$os GOARCH=$arch go build -o "$OUTPUT_DIR/$output_name" -ldflags "-X 'main.version=$VERSION' -X 'main.commit=$COMMIT' -X 'main.buildDate=$BUILD_DATE'" ./apkd

    if [ $? -eq 0 ]; then
        echo "Successfully built $output_name"
    else
        echo "Failed to build $output_name"
    fi
}

build "windows" "amd64" "apkd-$VERSION-windows-amd64.exe"
build "linux" "amd64" "apkd-$VERSION-linux-amd64"

echo "Build process completed."