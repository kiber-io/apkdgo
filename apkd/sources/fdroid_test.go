package sources

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testFDroidData() map[string]any {
	return map[string]any{
		"Com.Example.App": map[string]any{
			"metadata": map[string]any{
				"authorName": "Acme",
			},
			"versions": map[string]any{
				"stable": map[string]any{
					"file": map[string]any{
						"name": "example-v1.apk",
						"size": 10,
					},
					"manifest": map[string]any{
						"versionName": "1.0.0",
						"versionCode": 1,
					},
				},
			},
		},
		"com.other.app": map[string]any{
			"metadata": map[string]any{
				"authorName": "Other",
			},
			"versions": map[string]any{},
		},
	}
}

func TestFDroidGetAppInfoCaseInsensitive(t *testing.T) {
	s := &FDroid{}
	appInfo, err := s.getAppInfo(testFDroidData(), "com.example.app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if appInfo.PackageName != "Com.Example.App" {
		t.Fatalf("expected package name %q, got %q", "Com.Example.App", appInfo.PackageName)
	}
	if appInfo.Metadata.AuthorName != "Acme" {
		t.Fatalf("expected author %q, got %q", "Acme", appInfo.Metadata.AuthorName)
	}
}

func TestFDroidGetAppInfoNotFound(t *testing.T) {
	s := &FDroid{}
	_, err := s.getAppInfo(testFDroidData(), "missing.package")
	if err == nil {
		t.Fatalf("expected app-not-found error")
	}
	var appNotFoundErr *AppNotFoundError
	if !errors.As(err, &appNotFoundErr) {
		t.Fatalf("expected AppNotFoundError, got %T", err)
	}
}

func TestFDroidFindAllPackagesByAuthor(t *testing.T) {
	s := &FDroid{}
	apps, err := s.findAllPackagesByAuthor(testFDroidData(), "Acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected exactly 1 app, got %d", len(apps))
	}
	if apps[0].PackageName != "Com.Example.App" {
		t.Fatalf("unexpected package name: %q", apps[0].PackageName)
	}
}

func TestFDroidFindNeededVersion(t *testing.T) {
	s := &FDroid{}
	appInfo := AppInfo{
		PackageName: "com.example.app",
		Metadata:    AppMetadata{AuthorName: "Acme"},
		Versions: map[string]VersionJson{
			"v1": {
				File: VersionFile{Name: "example-v1.apk", Size: 10},
				Manifest: VersionManifest{
					VersionName: "1.0.0",
					VersionCode: 1,
				},
			},
			"v2": {
				File: VersionFile{Name: "example-v2.apk", Size: 20},
				Manifest: VersionManifest{
					VersionName: "2.0.0",
					VersionCode: 2,
				},
			},
		},
	}

	latest, err := s.findNeededVersion(appInfo, 0)
	if err != nil {
		t.Fatalf("unexpected error for latest version: %v", err)
	}
	if latest.Code != 2 || latest.Name != "2.0.0" || latest.Link != "example-v2.apk" {
		t.Fatalf("unexpected latest version: %+v", latest)
	}
	if latest.Type != APK {
		t.Fatalf("expected file type APK, got %q", latest.Type)
	}

	v1, err := s.findNeededVersion(appInfo, 1)
	if err != nil {
		t.Fatalf("unexpected error for explicit version: %v", err)
	}
	if v1.Code != 1 || v1.Name != "1.0.0" || v1.Link != "example-v1.apk" {
		t.Fatalf("unexpected explicit version: %+v", v1)
	}
}

func TestFDroidFindNeededVersionNotFound(t *testing.T) {
	s := &FDroid{}
	appInfo := AppInfo{
		PackageName: "com.example.app",
		Versions: map[string]VersionJson{
			"v1": {Manifest: VersionManifest{VersionCode: 1}},
		},
	}

	_, err := s.findNeededVersion(appInfo, 999)
	if err == nil {
		t.Fatalf("expected app-not-found error")
	}
	var appNotFoundErr *AppNotFoundError
	if !errors.As(err, &appNotFoundErr) {
		t.Fatalf("expected AppNotFoundError, got %T", err)
	}
}

func TestFDroidGetJsonClosesBodyOnSuccess(t *testing.T) {
	body := &trackingReadCloser{
		Reader: strings.NewReader(`{"packages":{"com.example.app":{"metadata":{"authorName":"Acme"},"versions":{}}}}`),
	}
	s := &FDroid{}
	s.Source = s
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       body,
			Request:    req,
		}, nil
	})

	_, err := s.getJson()
	if err != nil {
		t.Fatalf("unexpected getJson error: %v", err)
	}
	if !body.closed {
		t.Fatalf("expected response body to be closed")
	}
}

func TestFDroidGetJsonClosesBodyOnNon200(t *testing.T) {
	body := &trackingReadCloser{
		Reader: strings.NewReader("bad gateway"),
	}
	s := &FDroid{}
	s.Source = s
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Header:     http.Header{},
			Body:       body,
			Request:    req,
		}, nil
	})

	_, err := s.getJson()
	if err == nil {
		t.Fatalf("expected getJson error for non-200 response")
	}
	if !body.closed {
		t.Fatalf("expected response body to be closed on non-200 response")
	}
}

func TestFDroidGetJsonCachesResult(t *testing.T) {
	calls := 0
	const body = `{"packages":{"com.example":{"metadata":{},"versions":{}}}}`
	s := &FDroid{}
	s.Source = s
	s.config = defaultFDroidConfig()
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})

	if _, err := s.getJson(); err != nil {
		t.Fatalf("first getJson error: %v", err)
	}
	if _, err := s.getJson(); err != nil {
		t.Fatalf("second getJson error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 HTTP call, got %d", calls)
	}
}

func TestFDroidDownloadConstructsURL(t *testing.T) {
	const customBase = "https://custom.fdroid.example"
	const link = "/com.example_10.apk"
	var capturedURL string
	s := &FDroid{}
	s.Source = s
	s.config = FDroidConfig{BaseSourceConfig: BaseSourceConfig{BaseURL: customBase}, AppVersion: "1.0"}
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data")), ContentLength: 4, Request: req}, nil
	})

	stream, err := s.Download(Version{Link: link})
	if err != nil {
		t.Fatalf("unexpected download error: %v", err)
	}
	defer stream.Body.Close()
	if capturedURL != customBase+"/repo"+link {
		t.Fatalf("expected URL %q, got %q", customBase+"/repo"+link, capturedURL)
	}
}

func TestFDroidIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test")
	}
	setupTestProxy(t)
	setClientTimeout(t, 90*time.Second) // index-v2.json is ~30MB, 30s default isn't enough
	src, err := newFDroidSource()
	if err != nil {
		t.Fatalf("failed to create source: %v", err)
	}
	v, err := src.FindByPackage("org.fdroid.fdroid", 0)
	if err != nil {
		t.Fatalf("FindByPackage: %v", err)
	}
	if v.Code == 0 || v.Name == "" || v.Link == "" {
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
