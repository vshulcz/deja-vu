package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gooseConf(t *testing.T, cfg string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cfg, "goose", "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml missing: %v", err)
	}
	return string(b)
}

func TestInstallGooseWritesTheExtension(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if _, err := installGoose("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, cfg)
	// Goose keys extensions by name and skips anything not enabled; a stdio
	// entry without cmd is rejected at load.
	for _, want := range []string{"extensions:", "  deja:", "enabled: true", "type: stdio", "cmd: /bin/deja"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("config missing %q:\n%s", want, conf)
		}
	}
}

func TestInstallGooseKeepsOtherExtensions(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir := filepath.Join(cfg, "goose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "GOOSE_PROVIDER: openai\nextensions:\n  memory:\n    enabled: true\n    type: builtin\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGoose("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, cfg)
	for _, want := range []string{"GOOSE_PROVIDER: openai", "  memory:", "type: builtin", "  deja:"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("install dropped %q:\n%s", want, conf)
		}
	}
	if _, err := installGoose("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	conf = gooseConf(t, cfg)
	if strings.Contains(conf, "deja") {
		t.Fatalf("uninstall left our entry:\n%s", conf)
	}
	if !strings.Contains(conf, "  memory:") || !strings.Contains(conf, "GOOSE_PROVIDER: openai") {
		t.Fatalf("uninstall took the user's config with it:\n%s", conf)
	}
}

// .goosehints is read once when a session starts, so the file has to exist
// before the first run rather than being written on demand.
func TestInstallGooseAutoWritesHints(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hints := filepath.Join(cfg, "goose", ".goosehints")
	if _, err := os.Stat(hints); err != nil {
		t.Fatalf("hints not written: %v", err)
	}
	if _, err := installGooseAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(hints); !os.IsNotExist(err) {
		t.Fatalf("hints survived uninstall: %v", err)
	}
}

// Goose has no hooks — the only lifecycle strings in the binary belong to its
// Nushell integration — so an install that writes one would be dead wiring.
func TestInstallGooseWritesNoHooks(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if strings.Contains(gooseConf(t, cfg), "hook") {
		t.Fatal("config gained a hook Goose does not run")
	}
}
