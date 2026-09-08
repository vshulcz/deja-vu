package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A second install that changes nothing still has a snapshot beside every file
// it wrote the first time, and the kept-snapshot line reads the paths from the
// result. `wroteAll` dropped a path whose action was "unchanged", so the second
// run of grok-auto reported only the hooks file and the two configs vanished
// from the accounting — the same shape #3389 fixed one layer down (review).
func TestASecondInstallStillNamesEveryFileItWrote(t *testing.T) {
	tmp := hermeticEnv(t)
	grok := filepath.Join(tmp, "grok")
	if err := os.MkdirAll(grok, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", grok)
	t.Setenv("GROK_HOME", grok)

	first, err := installTarget("grok-auto", "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.touched()) < 3 {
		t.Fatalf("first run touched %v, want the config, the settings and the hooks file", first.touched())
	}
	second, err := installTarget("grok-auto", "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(second.touched(), " ")
	for _, want := range []string{"config.toml", "user-settings.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("second run touched %v, want %s among them", second.touched(), want)
		}
	}
}
