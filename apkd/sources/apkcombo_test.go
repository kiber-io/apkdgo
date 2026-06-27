package sources

import "testing"

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
