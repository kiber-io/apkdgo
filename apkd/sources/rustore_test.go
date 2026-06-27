package sources

import (
	"archive/zip"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kiber-io/apkd/apkd/devices"
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
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9') { //nolint:staticcheck // QF1001: equivalent De Morgan forms
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
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
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
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
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

func TestDecodeRuStoreConfigDefaults(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("{}"), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}
	configAny, err := DecodeSourceConfig("rustore", node.Content[0])
	if err != nil {
		t.Fatalf("unexpected config decode error: %v", err)
	}
	config, ok := configAny.(RuStoreConfig)
	if !ok {
		t.Fatalf("unexpected config type: %T", configAny)
	}
	expected := defaultRuStoreConfig()
	if !reflect.DeepEqual(config, expected) {
		t.Fatalf("unexpected default config: got=%+v expected=%+v", config, expected)
	}
}

func TestDecodeRuStoreConfigUnknownField(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("{bad: value}"), &node); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}
	if _, err := DecodeSourceConfig("rustore", node.Content[0]); err == nil {
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

const (
	ruStoreOKAppInfo      = `{"code":"OK","body":{"appId":1,"fileSize":123456,"versionName":"1.0.0","versionCode":100,"publicCompanyId":"dev123"}}`
	ruStoreOKDownloadLink = `{"code":"OK","body":{"downloadUrls":[{"url":"https://cdn.example.com/app.apk"}]}}`
	ruStoreOKDevApps      = `{"code":"OK","body":{"elements":[{"packageName":"com.app.one"},{"packageName":"com.app.two"}]}}`
)

func mockRuStore(doer doerFunc) *RuStore {
	s := &RuStore{appsCache: make(map[string]map[string]any)}
	s.Source = s
	s.config = defaultRuStoreConfig()
	s.Net = doer
	return s
}

func okResp(req *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func statusResp(req *http.Request, code int) *http.Response {
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}
}

func TestRuStoreGetAppInfoCacheHit(t *testing.T) {
	calls := 0
	s := &RuStore{appsCache: map[string]map[string]any{"com.cached": {"appId": 1.0}}}
	s.Source = s
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return okResp(req, "{}"), nil
	})
	if _, err := s.getAppInfo("com.cached"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected 0 HTTP calls for cached entry, got %d", calls)
	}
}

func TestRuStoreGetAppInfoNotFound(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return statusResp(req, http.StatusNotFound), nil
	})
	_, err := s.getAppInfo("com.missing")
	var notFound *AppNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected AppNotFoundError, got %T: %v", err, err)
	}
}

func TestRuStoreGetAppInfoNon200(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return statusResp(req, http.StatusInternalServerError), nil
	})
	if _, err := s.getAppInfo("com.example"); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestRuStoreGetAppInfoInvalidJSON(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return okResp(req, "not json"), nil
	})
	if _, err := s.getAppInfo("com.example"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRuStoreGetAppInfoCodeNotOK(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return okResp(req, `{"code":"ERROR","message":"not found"}`), nil
	})
	_, err := s.getAppInfo("com.example")
	var notFound *AppNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected AppNotFoundError for code!=OK, got %T: %v", err, err)
	}
}

func TestRuStoreFindByPackageHappyPath(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return okResp(req, ruStoreOKAppInfo), nil
	})
	v, err := s.FindByPackage("com.example", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Name != "1.0.0" || v.Code != 100 || v.Size != 123456 || v.DeveloperId != "dev123" || v.Type != APK {
		t.Fatalf("unexpected version: %+v", v)
	}
}

func TestRuStoreFindByPackageVersionCodeMismatch(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return okResp(req, ruStoreOKAppInfo), nil
	})
	_, err := s.FindByPackage("com.example", 999)
	var notFound *AppNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected AppNotFoundError for version code mismatch, got %T: %v", err, err)
	}
}

func TestRuStoreGetDownloadLinkHappyPath(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return okResp(req, ruStoreOKDownloadLink), nil
	})
	link, err := s.getDownloadLink(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if link != "https://cdn.example.com/app.apk" {
		t.Fatalf("unexpected download link: %q", link)
	}
}

func TestRuStoreGetDownloadLinkNotFound(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return statusResp(req, http.StatusNotFound), nil
	})
	_, err := s.getDownloadLink(1)
	var notFound *AppNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected AppNotFoundError for 404, got %T: %v", err, err)
	}
}

func TestRuStoreFindByDeveloperHappyPath(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return okResp(req, ruStoreOKDevApps), nil
	})
	pkgs, err := s.FindByDeveloper("dev123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pkgs) != 2 || pkgs[0] != "com.app.one" || pkgs[1] != "com.app.two" {
		t.Fatalf("unexpected packages: %v", pkgs)
	}
}

func TestRuStoreFindByDeveloperNotFound(t *testing.T) {
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return statusResp(req, http.StatusNotFound), nil
	})
	_, err := s.FindByDeveloper("nobody")
	var notFound *AppNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected AppNotFoundError for 404, got %T: %v", err, err)
	}
}

func TestRuStoreGetLatestVersionParsesResponse(t *testing.T) {
	const body = `{"body":{"latestVersion":"1103100","latestVersionName":"1.103.1.0"}}`
	s := mockRuStore(func(req *http.Request) (*http.Response, error) {
		return okResp(req, body), nil
	})
	update, err := s.getLatestRustoreVersion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if update.Body.LatestVersionCode != "1103100" || update.Body.LatestVersionName != "1.103.1.0" {
		t.Fatalf("unexpected update: %+v", update.Body)
	}
}

func TestBuildUserAgent(t *testing.T) {
	device := devices.Device{
		AndroidVersion: "12",
		SDKInt:         31,
		CPUAbis:        []string{"arm64-v8a"},
		Manufacturer:   "Google",
		Model:          "Pixel 6",
	}
	ua := buildUserAgent("1.103.1.0", device)
	for _, want := range []string{"RuStore/1.103.1.0", "Android 12", "SDK 31", "arm64-v8a", "Google", "Pixel 6"} {
		if !strings.Contains(ua, want) {
			t.Fatalf("expected %q in user agent %q", want, ua)
		}
	}
}

func TestRuStoreIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test")
	}
	setupTestProxy(t)
	src, err := newRuStoreSource()
	if err != nil {
		t.Fatalf("failed to create source: %v", err)
	}
	v, err := src.FindByPackage("com.vkontakte.android", 0)
	if err != nil {
		t.Fatalf("FindByPackage: %v", err)
	}
	if v.Code == 0 || v.Name == "" {
		t.Fatalf("expected non-empty version, got %+v", v)
	}
	t.Logf("version: %s (%d), size: %d", v.Name, v.Code, v.Size)

	stream, err := src.Download(v)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer stream.Body.Close()
	buf := make([]byte, 32)
	n, _ := stream.Body.Read(buf)
	if n < 4 || buf[0] != 'P' || buf[1] != 'K' {
		t.Fatalf("expected APK/ZIP magic (PK), got first %d bytes: %q", n, buf[:n])
	}
	t.Logf("download started OK, first %d bytes received", n)
}
