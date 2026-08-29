package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Installing for Gemini turns on hooksConfig.enabled, a switch that belongs to
// the harness rather than to deja. Uninstall leaves it on for a good reason —
// another extension may be running on it — and said nothing, while naming every
// other thing it kept (#2487).
func TestUninstallSaysItLeftGeminiHooksOn(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"GitHub"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = home

	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	res, err := installGeminiAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Fatalf("this test is about the switch staying on; it is off:\n%s", b)
	}
	if !strings.Contains(res.Note, "hooksConfig") {
		t.Errorf("uninstall left a harness-wide switch on and its note does not mention it: %q", res.Note)
	}
}

// Nothing to say when deja never turned it on.
func TestUninstallIsQuietWhenGeminiHooksAreOff(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	_ = home
	res, err := installGeminiAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Note, "hooksConfig") {
		t.Errorf("nothing was left on, but uninstall says %q", res.Note)
	}
}
