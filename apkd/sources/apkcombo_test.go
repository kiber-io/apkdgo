package sources

import (
	"io"
	"net/http"
	"os"
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
	if _, err := parseVersionCodeText("0"); err == nil {
		t.Fatalf("expected parse error for non-positive version code")
	}
}

func TestApkComboProfileBaseURLIsUsed(t *testing.T) {
	s := &ApkCombo{config: ApkComboConfig{BaseSourceConfig: BaseSourceConfig{BaseURL: "https://mirror.example"}}}
	if s.config.BaseURL != "https://mirror.example" {
		t.Fatalf("expected custom baseURL to be preserved")
	}
}

func TestParseApkComboVersionName(t *testing.T) {
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"Version 1.2.3", "1.2.3", false},
		{"APK 10.0.0-beta", "10.0.0-beta", false},
		{"  spaces  1.0 ", "1.0", false},
		{"single", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := parseApkComboVersionName(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseApkComboVersionName(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseApkComboVersionName(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseApkComboVersionName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseApkComboFileType(t *testing.T) {
	if ft, err := parseApkComboFileType("APK"); err != nil || ft != APK {
		t.Fatalf("expected APK, got %q %v", ft, err)
	}
	if ft, err := parseApkComboFileType("XAPK"); err != nil || ft != XAPK {
		t.Fatalf("expected XAPK, got %q %v", ft, err)
	}
	if _, err := parseApkComboFileType("OTHER"); err == nil {
		t.Fatal("expected error for unknown file type")
	}
}

func TestApkComboCheckinUsesBaseURL(t *testing.T) {
	const customBase = "https://custom.apkcombo.example"
	var capturedURL string
	s := &ApkCombo{}
	s.Source = s
	s.config = ApkComboConfig{BaseSourceConfig: BaseSourceConfig{BaseURL: customBase}}
	s.Net = doerFunc(func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("token=abc")), Request: req}, nil
	})
	if _, err := s.checkin("https://referer.example"); err != nil {
		t.Fatalf("unexpected checkin error: %v", err)
	}
	if capturedURL != customBase+"/checkin" {
		t.Fatalf("expected checkin URL %q, got %q", customBase+"/checkin", capturedURL)
	}
}

func TestApkComboIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test")
	}
	if os.Getenv("APKD_TEST_APKCOMBO") == "" {
		t.Skip("skipping: set APKD_TEST_APKCOMBO=1 to enable (web scraping, may be flaky in CI)")
	}
	setupTestProxy(t)
	src, err := newApkComboSource()
	if err != nil {
		t.Fatalf("failed to create source: %v", err)
	}
	v, err := src.FindByPackage("org.fdroid.fdroid", 0)
	if err != nil {
		t.Fatalf("FindByPackage: %v", err)
	}
	if v.Code == 0 || v.Name == "" {
		t.Fatalf("expected non-empty version, got %+v", v)
	}
	t.Logf("version: %s (%d)", v.Name, v.Code)

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
