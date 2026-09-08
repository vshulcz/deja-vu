package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dsh's generated layer is a comment and the empty array. Uninstall dropped
// the array and kept the comment, and a YAML file that is only a comment
// parses as null, which dsh refuses: "must be a top-level YAML array of loader
// patch entries" (#3271).
func TestUninstallDeepSeekKeepsTheLayerLoadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	path := filepath.Join(home, ".dsh", "cordis.patch.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "# dsh profile root — an empty entry list. Edit cordis.patch.yml, not this file.\n[]\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if lines[0] != strings.Split(seed, "\n")[0] {
		t.Errorf("the comment did not survive:\n%s", got)
	}
	if lines[len(lines)-1] != "[]" {
		t.Errorf("the layer has no array after uninstall, dsh cannot load it:\n%s", got)
	}
}
