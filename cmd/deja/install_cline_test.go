package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallClineWritesAPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Plugins sit beside the data directory, not inside it: a plugin under
	// ~/.cline/data/plugins is never discovered.
	path := filepath.Join(home, ".cline", "plugins", "deja", "index.js")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plugin not written where cline looks: %v", err)
	}
	js := string(b)
	// The registry validates the manifest before setup runs and rejects
	// contributions that were not declared.
	for _, want := range []string{`capabilities: ["rules", "commands"]`, "registerRule", "registerCommand"} {
		if !strings.Contains(js, want) {
			t.Fatalf("plugin missing %q:\n%s", want, js)
		}
	}
	// A string here would freeze whatever history existed at install time.
	// Only the function form is resolved per session.
	if !strings.Contains(js, "content: () =>") {
		t.Fatalf("rule content is not lazy, recall would never update:\n%s", js)
	}
	if !strings.Contains(js, `hook-context`) {
		t.Fatalf("plugin does not call the context hook:\n%s", js)
	}
}

// Cline hooks look like the obvious channel and are not one: their stdout is
// closed and no hook output can carry context. Writing one would leave a dead
// integration that looks installed.
func TestInstallClineWritesNoHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(home, ".cline", "hooks"),
		filepath.Join(home, "Documents", "Cline", "Hooks"),
	} {
		if _, err := os.Stat(dir); err == nil {
			t.Fatalf("install created %s; cline hook output is discarded", dir)
		}
	}
}

func TestInstallClineIsIdempotentAndRemovable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	first, err := installClineAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if first.Action != "created" {
		t.Fatalf("first install action = %q", first.Action)
	}
	again, err := installClineAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if again.Action != "unchanged" {
		t.Fatalf("reinstall rewrote the plugin: %q", again.Action)
	}
	if _, err := installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cline", "plugins", "deja")); !os.IsNotExist(err) {
		t.Fatalf("plugin survived uninstall: %v", err)
	}
}
