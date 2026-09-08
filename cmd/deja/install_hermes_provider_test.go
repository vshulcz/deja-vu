package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The provider deja writes has to load on the Hermes it finds, and the hook
// has to know whether it did. On 0.17 the generated file imported a name that
// build does not define, so the provider never loaded — while the hook stood
// down because the config named it (#3390).
func TestHermesProviderLoadsWithoutRecallStatus(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)
	if _, err := installHermesMemoryProvider("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "plugins", "deja-memory", "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "import MemoryProvider, RecallStatus") {
		t.Error("the provider imports RecallStatus unconditionally; a build without it cannot load the file at all")
	}
	if !strings.Contains(src, "RecallStatus = None") {
		t.Error("nothing stands in for RecallStatus on a build that lacks it")
	}
}

// And the hook asks whether the provider loaded rather than whether the config
// names it, or it stands down for a provider that answers nothing.
func TestHermesHookAsksTheLoaderNotTheConfig(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)
	if _, err := installHermesPlugin("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "plugins", "deja", "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "load_memory_provider") {
		t.Error("_provider_active reads the config alone; a provider that failed to load still silences the hook")
	}
}
