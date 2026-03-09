package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kiber-io/apkd/apkd/sources"
)

func TestSanitizeFileNameReplacesInvalidCharsAndTrims(t *testing.T) {
	got := sanitizeFileName(`  app<>:"/\|?*name.apk  `)
	if got != "app-name.apk" {
		t.Fatalf("expected sanitized name %q, got %q", "app-name.apk", got)
	}
}

func TestSanitizeFileNameLimitsLength(t *testing.T) {
	input := strings.Repeat("a", 300)
	got := sanitizeFileName(input)
	if len(got) != 255 {
		t.Fatalf("expected sanitized name length 255, got %d", len(got))
	}
}

func TestSanitizedAndAbsoluteNameValid(t *testing.T) {
	name := filepath.Join(t.TempDir(), "app.apk")

	got, err, warn := sanitizedAndAbsoluteName(name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn != nil {
		t.Fatalf("unexpected warning: %v", warn)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
	if filepath.Base(got) != "app.apk" {
		t.Fatalf("expected file name %q, got %q", "app.apk", filepath.Base(got))
	}
}

func TestSanitizedAndAbsoluteNameWarnsOnInvalidName(t *testing.T) {
	name := filepath.Join(t.TempDir(), "bad:name.apk")

	got, err, warn := sanitizedAndAbsoluteName(name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn == nil {
		t.Fatalf("expected warning for invalid file name")
	}
	if filepath.Base(got) != "bad-name.apk" {
		t.Fatalf("expected file name %q, got %q", "bad-name.apk", filepath.Base(got))
	}
}

func TestParseSourceProxyEntries(t *testing.T) {
	got, err := parseSourceProxyEntries([]string{
		"rustore=http://127.0.0.1:8080",
		"FDROID=http://127.0.0.1:8081",
	})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	expected := map[string]string{
		"rustore": "http://127.0.0.1:8080",
		"fdroid":  "http://127.0.0.1:8081",
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("unexpected parsed source proxies: got=%v expected=%v", got, expected)
	}
}

func TestParseSourceProxyEntriesInvalid(t *testing.T) {
	if _, err := parseSourceProxyEntries([]string{"rustore:http://127.0.0.1:8080"}); err == nil {
		t.Fatalf("expected parse error for invalid entry format")
	}
}

func TestValidateKnownSourcesValid(t *testing.T) {
	allSources := map[string]sources.Source{
		"fdroid":  nil,
		"rustore": nil,
	}
	err := validateKnownSources([]string{"fdroid"}, map[string]string{
		"rustore": "http://127.0.0.1:8080",
	}, allSources)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateKnownSourcesUnknownSelected(t *testing.T) {
	allSources := map[string]sources.Source{
		"fdroid": nil,
	}
	err := validateKnownSources([]string{"proxy"}, nil, allSources)
	if err == nil {
		t.Fatalf("expected validation error for unknown --source value")
	}
	if !strings.Contains(err.Error(), "--source: proxy") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestValidateKnownSourcesUnknownSourceProxy(t *testing.T) {
	allSources := map[string]sources.Source{
		"fdroid": nil,
	}
	err := validateKnownSources([]string{"fdroid"}, map[string]string{
		"proxy": "http://127.0.0.1:8080",
	}, allSources)
	if err == nil {
		t.Fatalf("expected validation error for unknown --source-proxy value")
	}
	if !strings.Contains(err.Error(), "--source-proxy: proxy") {
		t.Fatalf("unexpected error text: %v", err)
	}
}
