package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPiExtensionWritesDiscoverableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installPiExtension("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// pi auto-discovers ~/.pi/agent/extensions/*.ts; anywhere else is inert.
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("extension not at the discovered path: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"before_agent_start", // the only event that can inject a message
		"hook-context",
		"hook-prompt",
		"ctx.ui.notify",
		`"/bin/deja"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("extension missing %q:\n%s", want, src)
		}
	}
}

func TestInstallPiExtensionRemovesOnUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	if _, err := installPiExtension("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installPiExtension("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("extension survived uninstall: %v", err)
	}
	// Uninstalling twice is not an error.
	if r, err := installPiExtension("/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestPiExtensionQuotesExecutablePath(t *testing.T) {
	// A path with a quote or backslash must not break out of the TS string.
	src := piExtensionTS(`/tmp/we"ird\path/deja`)
	if !strings.Contains(src, `const DEJA = "/tmp/we\"ird\\path/deja";`) {
		t.Fatalf("executable path not escaped:\n%s", src)
	}
}
