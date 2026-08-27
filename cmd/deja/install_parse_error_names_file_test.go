package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A config deja cannot parse makes the target refuse, and the refusal used to
// be a bare parser error: "unexpected end of JSON input — fix what it reports
// and run it again", with the path deja had just read left out. It is the end
// of the trail doctor sends people down after a failed rewire (#2214).
func TestInstallNamesTheConfigItCannotParse(t *testing.T) {
	hermeticEnv(t)
	claudeJSON := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("claude", "/some/deja", false); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: this file parses today, so the refusal below is about the
	// damage and not about the fixture.
	if _, err := installTarget("claude", "/some/deja", false); err != nil {
		t.Fatalf("a config deja just wrote does not parse: %v", err)
	}

	if err := os.WriteFile(claudeJSON, good[:len(good)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = installTarget("claude", "/some/deja", false)
	if err == nil {
		t.Fatal("a truncated config was accepted")
	}
	if !strings.Contains(err.Error(), claudeJSON) {
		t.Errorf("the refusal names no file, so nobody knows what to fix: %v", err)
	}
	// The parser's own words stay: they say what is wrong with it.
	if !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Errorf("the refusal dropped what the parser said: %v", err)
	}

	// The same for the other writers that read a config of their own.
	settings := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeJSON, good, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"hooks":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("claude-auto", "/some/deja", false); err == nil {
		t.Error("a truncated settings.json was accepted")
	} else if !strings.Contains(err.Error(), settings) {
		t.Errorf("the refusal names no file: %v", err)
	}
}
