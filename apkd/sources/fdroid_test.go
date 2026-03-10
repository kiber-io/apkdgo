package sources

import (
	"net/http"
	"strings"
	"testing"
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
	if _, ok := err.(*AppNotFoundError); !ok {
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
	if _, ok := err.(*AppNotFoundError); !ok {
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
