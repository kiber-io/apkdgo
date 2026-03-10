package sources

import (
	"net/http"
	"strings"
	"testing"
)

func TestParseVersionCodeText(t *testing.T) {
	versionCode, err := parseVersionCodeText("(123)")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if versionCode != 123 {
		t.Fatalf("expected parsed version code 123, got %d", versionCode)
	}

	versionCode, err = parseVersionCodeText("123")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if versionCode != 123 {
		t.Fatalf("expected parsed version code 123, got %d", versionCode)
	}
}

func TestParseVersionCodeTextMalformed(t *testing.T) {
	if _, err := parseVersionCodeText("("); err == nil {
		t.Fatalf("expected parse error for malformed version code text")
	}
	if _, err := parseVersionCodeText("()"); err == nil {
		t.Fatalf("expected parse error for empty version code text")
	}
}

func TestApkComboFindByPackageClosesResponseBodies(t *testing.T) {
	searchBody := &trackingReadCloser{
		Reader: strings.NewReader(`<html><div id="icon-arrow-download"></div><div class="author"><a class="is-link">Dev</a></div></html>`),
	}
	oldVersionsBody := &trackingReadCloser{
		Reader: strings.NewReader(`<html><a class="ver-item" href="/ver1">v1</a></html>`),
	}
	versionBody := &trackingReadCloser{
		Reader: strings.NewReader(
			`<html><div class="variant" href="/download/apk"><span class="vercode">(123)</span><span class="vername">Version 1.2.3</span><div class="description"><span class="spec ltr">10 MB</span></div><div class="vtype"><span>APK</span></div></div></html>`,
		),
	}

	s := &ApkCombo{}
	s.Source = s
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/search":
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{},
				Body:       searchBody,
				Request:    req,
			}, nil
		case "/search/old-versions":
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{},
				Body:       oldVersionsBody,
				Request:    req,
			}, nil
		case "/ver1":
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{},
				Body:       versionBody,
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected URL path %q", req.URL.Path)
			return nil, nil
		}
	})

	version, err := s.FindByPackage("com.example.app", 0)
	if err != nil {
		t.Fatalf("unexpected FindByPackage error: %v", err)
	}
	if version.Code != 123 {
		t.Fatalf("expected version code 123, got %d", version.Code)
	}
	if !searchBody.closed {
		t.Fatalf("expected search response body to be closed")
	}
	if !oldVersionsBody.closed {
		t.Fatalf("expected old versions response body to be closed")
	}
	if !versionBody.closed {
		t.Fatalf("expected version details response body to be closed")
	}
}
