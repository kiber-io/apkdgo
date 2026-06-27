package sources

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNashStoreGetAppInfoInvalidSizeTypeReturnsError(t *testing.T) {
	s := &NashStore{}
	s.Source = s
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       ioNopCloser(`{"list":[{"app_id":"com.example.app","id":"dev","release":{"version_code":1,"version_name":"1.0.0","install_path":"https://example.com/app.apk"},"size":"oops"}]}`),
			Request:    req,
		}, nil
	})

	_, err := s.getAppInfo("com.example.app")
	if err == nil {
		t.Fatalf("expected getAppInfo error for invalid size type")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "size") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestNashStoreFindByDeveloperInvalidAppShapeReturnsError(t *testing.T) {
	s := &NashStore{}
	s.Source = s
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       ioNopCloser(`{"app":"bad-shape"}`),
			Request:    req,
		}, nil
	})

	_, err := s.FindByDeveloper("dev")
	if err == nil {
		t.Fatalf("expected FindByDeveloper error for invalid app shape")
	}
}

func TestNashStoreFindByDeveloperHandlesEmptyOtherApps(t *testing.T) {
	s := &NashStore{}
	s.Source = s
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       ioNopCloser(`{"app":{"other_apps":{"apps":[]}}}`),
			Request:    req,
		}, nil
	})

	packages, err := s.FindByDeveloper("dev")
	if err != nil {
		t.Fatalf("unexpected FindByDeveloper error: %v", err)
	}
	if len(packages) != 0 {
		t.Fatalf("expected empty package list, got %v", packages)
	}
}

func ioNopCloser(body string) *trackingReadCloser {
	return &trackingReadCloser{Reader: strings.NewReader(body)}
}

const nashStoreHappyBody = `{"list":[{"app_id":"com.example","id":"dev42","release":{"version_code":10,"version_name":"1.0.0","install_path":"https://cdn.example.com/app.apk"},"size":99000}]}`

func mockNashStore(doer doerFunc) *NashStore {
	s := &NashStore{}
	s.Source = s
	s.config = defaultNashStoreConfig()
	s.Net = doer
	return s
}

func TestNashStoreFindByPackageHappyPath(t *testing.T) {
	s := mockNashStore(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(nashStoreHappyBody)), Request: req}, nil
	})
	v, err := s.FindByPackage("com.example", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Code != 10 || v.Name != "1.0.0" || v.Size != 99000 || v.DeveloperId != "dev42" || v.Type != APK {
		t.Fatalf("unexpected version: %+v", v)
	}
	if v.Link != "https://cdn.example.com/app.apk" {
		t.Fatalf("unexpected link: %q", v.Link)
	}
}

func TestNashStoreFindByPackageNotFound(t *testing.T) {
	const emptyBody = `{"list":[]}`
	s := mockNashStore(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(emptyBody)), Request: req}, nil
	})
	_, err := s.FindByPackage("com.missing", 0)
	var notFound *AppNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected AppNotFoundError, got %T: %v", err, err)
	}
}

func TestNashStoreFindByPackageVersionCodeMismatch(t *testing.T) {
	s := mockNashStore(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(nashStoreHappyBody)), Request: req}, nil
	})
	_, err := s.FindByPackage("com.example", 999)
	var notFound *AppNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected AppNotFoundError for version code mismatch, got %T: %v", err, err)
	}
}

func TestNashStoreGetAppInfoNon200(t *testing.T) {
	s := mockNashStore(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Header: http.Header{}, Body: io.NopCloser(strings.NewReader("forbidden")), Request: req}, nil
	})
	if _, err := s.getAppInfo("com.example"); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestNashStoreGetAppInfoMultipleAppsReturnsError(t *testing.T) {
	const body = `{"list":[{"app_id":"com.a","id":"d","release":{},"size":1},{"app_id":"com.b","id":"d","release":{},"size":1}]}`
	s := mockNashStore(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	if _, err := s.getAppInfo("com.a"); err == nil {
		t.Fatal("expected error when multiple apps returned")
	}
}

func TestNashStoreIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test")
	}
	if !setupTestProxy(t) {
		t.Skip("skipping: set APKD_TEST_PROXY to a Russian proxy (NashStore is geo-restricted)")
	}
	src, err := newNashStoreSource()
	if err != nil {
		t.Fatalf("failed to create source: %v", err)
	}
	v, err := src.FindByPackage("com.bastion", 0)
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
