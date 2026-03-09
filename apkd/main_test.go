package main

import (
	"path/filepath"
	"strings"
	"testing"
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
