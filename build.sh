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

    GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -o "$OUTPUT_DIR/$output_name" -ldflags "-s -w -X 'main.version=$VERSION' -X 'main.commit=$COMMIT' -X 'main.buildDate=$BUILD_DATE'" -trimpath ./apkd

    if [ $? -eq 0 ]; then
        echo "Successfully built $output_name"
    else
        echo "Failed to build $output_name"
    fi
}

if [[ "$1" == "-a" ]]; then
    build "windows" "amd64" "apkd-$VERSION-windows-amd64.exe"
    build "windows" "386" "apkd-$VERSION-windows-386.exe"
    build "linux" "amd64" "apkd-$VERSION-linux-amd64"
    build "linux" "arm64" "apkd-$VERSION-linux-arm64"
    build "linux" "386" "apkd-$VERSION-linux-386"
    build "darwin" "amd64" "apkd-$VERSION-darwin-amd64"
    build "darwin" "arm64" "apkd-$VERSION-darwin-arm64"
else
    CUR_OS=$(go env GOOS)
    CUR_ARCH=$(go env GOARCH)
    build "$CUR_OS" "$CUR_ARCH" "apkd-$VERSION-$CUR_OS-$CUR_ARCH"
fi

echo "Build process completed."