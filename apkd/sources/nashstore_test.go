package sources

import (
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
