package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goose keys extensions by name. Writing deja's mapping entry under a key whose
// value is a sequence leaves a mapping and a sequence under one key, which no
// YAML parser accepts — and install said "updated" and exited 0 (#1697).
func TestInstallGooseRefusesAnExtensionsList(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_GOOSE_ROOT")
	if dir == "" {
		t.Skip("no goose root in this environment")
	}
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	const list = "extensions:\n  - developer\n  - fetch\n"
	if err := os.WriteFile(cfg, []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "install", "goose")
	if err == nil {
		t.Error("install accepted an extensions: list")
	} else if !strings.Contains(err.Error(), "extensions") {
		t.Errorf("the refusal does not name what it could not read: %s", err)
	}
	after, readErr := os.ReadFile(cfg)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != list {
		t.Errorf("install changed a config it could not read:\n%s", after)
	}
}

// An inline value is not a block: the insert missed it and appended a second
// `extensions:` key, and a parser takes the last of two — deja's — so the
// user's extensions vanished without a word (#1697).
func TestInstallGooseRefusesAnInlineExtensions(t *testing.T) {
	hermeticEnv(t)
	if os.Getenv("DEJA_GOOSE_ROOT") == "" {
		t.Skip("no goose root in this environment")
	}
	for _, inline := range []string{"extensions: [a, b]\n", "extensions: {a: {enabled: true}}\n"} {
		cfg := filepath.Join(gooseConfigDir(), "config.yaml")
		if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg, []byte(inline), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "goose"); err == nil {
			after, _ := os.ReadFile(cfg)
			t.Errorf("install accepted %q:\n%s", inline, after)
		}
		after, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != inline {
			t.Errorf("install changed a config it could not edit:\n%s", after)
		}
	}
}

// The control: a mapping — the shape goose actually uses — still gets deja's
// entry, keeps the user's, and survives a second install and an uninstall.
func TestInstallGooseKeepsAHandWrittenBlock(t *testing.T) {
	hermeticEnv(t)
	if os.Getenv("DEJA_GOOSE_ROOT") == "" {
		t.Skip("no goose root in this environment")
	}
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	const hand = "# mine\nGOOSE_PROVIDER: anthropic\nextensions:\n  # my own\n  developer:\n    enabled: true\n    type: builtin\n"
	if err := os.WriteFile(cfg, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "goose"); err != nil {
		t.Fatal(err)
	}
	names := gooseExtensionNames(t, cfg)
	if !names["deja"] || !names["developer"] {
		t.Fatalf("install lost an extension: %v", names)
	}
	first, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "goose"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("a second install rewrote the config:\n%s\n---\n%s", first, second)
	}
	if _, err := captureRun(t, "uninstall", "goose"); err != nil {
		t.Fatal(err)
	}
	names = gooseExtensionNames(t, cfg)
	if names["deja"] || !names["developer"] {
		t.Errorf("uninstall did not put the block back: %v", names)
	}
}

// gooseExtensionNames reads the entry names under `extensions:` and fails if
// the block mixes a mapping with a sequence — deja has no YAML parser, and
// pulling one in for a test would check a library rather than this code.
func gooseExtensionNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	inBlock := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "extensions:") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		// Only at the entry level: `- "mcp"` inside an entry's args is fine.
		if strings.HasPrefix(line, "  - ") {
			t.Fatalf("the extensions block holds a sequence item where entries go:\n%s", b)
		}
		// Entry names sit at exactly one level of indent; their fields deeper.
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(trim, ":") {
			names[strings.TrimSuffix(trim, ":")] = true
		}
	}
	return names
}
