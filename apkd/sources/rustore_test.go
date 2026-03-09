package sources

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func createZipFile(t *testing.T, path string, files map[string]string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create zip file: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
}

func TestRuStoreGenerateDeviceIDFormat(t *testing.T) {
	s := &RuStore{}
	id := s.generateDeviceId()
	parts := strings.Split(id, "-")
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts separated by '-', got %q", id)
	}
	if len(parts[0]) != 16 {
		t.Fatalf("expected first part length 16, got %d", len(parts[0]))
	}
	if len(parts[1]) != 10 {
		t.Fatalf("expected second part length 10, got %d", len(parts[1]))
	}
	for _, c := range parts[0] {
		if !('a' <= c && c <= 'z') && !('0' <= c && c <= '9') {
			t.Fatalf("unexpected char %q in first part", c)
		}
	}
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			t.Fatalf("unexpected char %q in second part", c)
		}
	}
}

func TestReplaceFileSafelySamePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	if err := replaceFileSafely(file, file); err != nil {
		t.Fatalf("unexpected error for same path: %v", err)
	}
}

func TestReplaceFileSafelyReplacesDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("new"), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to write destination file: %v", err)
	}

	if err := replaceFileSafely(src, dst); err != nil {
		t.Fatalf("unexpected replace error: %v", err)
	}

	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(body) != "new" {
		t.Fatalf("expected destination content %q, got %q", "new", string(body))
	}
	if _, err := os.Stat(dst + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("expected backup file to be removed, stat error: %v", err)
	}
}

func TestExtractApkFromZipWhenArchiveIsAlreadyAPK(t *testing.T) {
	s := &RuStore{}
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "app.download")
	outPath := filepath.Join(dir, "out.apk")

	createZipFile(t, zipPath, map[string]string{
		"AndroidManifest.xml": "<manifest/>",
		"classes.dex":         "dex",
	})

	if err := s.ExtractApkFromZip(zipPath, outPath); err != nil {
		t.Fatalf("unexpected extract error: %v", err)
	}
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Fatalf("expected source zip to be removed, stat error: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected output file to exist: %v", err)
	}

	r, err := zip.OpenReader(outPath)
	if err != nil {
		t.Fatalf("expected output to remain a valid zip/apk: %v", err)
	}
	defer r.Close()
	if len(r.File) == 0 {
		t.Fatalf("expected output archive to have files")
	}
}

func TestExtractApkFromZipExtractsEmbeddedApk(t *testing.T) {
	s := &RuStore{}
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "archive.download")
	outPath := filepath.Join(dir, "out.apk")
	createZipFile(t, zipPath, map[string]string{
		"payload/base.apk": "APKDATA",
		"readme.txt":       "text",
	})

	if err := s.ExtractApkFromZip(zipPath, outPath); err != nil {
		t.Fatalf("unexpected extract error: %v", err)
	}
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Fatalf("expected source zip to be removed, stat error: %v", err)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read extracted apk: %v", err)
	}
	if string(body) != "APKDATA" {
		t.Fatalf("expected extracted apk content %q, got %q", "APKDATA", string(body))
	}
}

func TestExtractApkFromZipReturnsErrorWhenNoApkFound(t *testing.T) {
	s := &RuStore{}
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "archive.download")
	outPath := filepath.Join(dir, "out.apk")
	createZipFile(t, zipPath, map[string]string{
		"readme.txt": "text",
	})

	err := s.ExtractApkFromZip(zipPath, outPath)
	if err == nil {
		t.Fatalf("expected error when archive has no apk")
	}
	if !strings.Contains(err.Error(), "no .apk file found") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestDecodeRuStoreProfileDefaults(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("{}"), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}
	profileAny, err := DecodeSourceProfile("rustore", node.Content[0])
	if err != nil {
		t.Fatalf("unexpected profile decode error: %v", err)
	}
	profile, ok := profileAny.(RuStoreProfile)
	if !ok {
		t.Fatalf("unexpected profile type: %T", profileAny)
	}
	expected := defaultRuStoreProfile()
	if profile != expected {
		t.Fatalf("unexpected default profile: got=%+v expected=%+v", profile, expected)
	}
}

func TestDecodeRuStoreProfileUnknownField(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("{bad: value}"), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}
	if _, err := DecodeSourceProfile("rustore", node.Content[0]); err == nil {
		t.Fatalf("expected decode error for unknown field")
	}
}

func TestRuStoreGetAppInfoConcurrentCacheAccess(t *testing.T) {
	const workers = 64
	const packageName = "com.example.app"
	const responseBody = `{"code":"OK","body":{"appId":1,"fileSize":123,"versionName":"1.0.0","versionCode":1,"publicCompanyId":"dev"}}`

	releaseResponses := make(chan struct{})
	s := &RuStore{
		appsCache: make(map[string]map[string]any),
	}
	s.Source = s
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		<-releaseResponses
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Request:    req,
		}, nil
	})

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.getAppInfo(packageName)
			errs <- err
		}()
	}
	close(releaseResponses)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected getAppInfo error: %v", err)
		}
	}

	s.appsCacheMu.RLock()
	_, exists := s.appsCache[packageName]
	cacheLen := len(s.appsCache)
	s.appsCacheMu.RUnlock()

	if !exists {
		t.Fatalf("expected package %q to be present in cache", packageName)
	}
	if cacheLen != 1 {
		t.Fatalf("expected cache size 1, got %d", cacheLen)
	}
}
