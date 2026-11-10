package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A 3.17+ desktop build reads config.json, so hooks have to reach it even
// when config.json has no hooks block at all yet (a fresh install on a machine
// whose runtime has not written one). The version is the primary signal; the
// file shape is only the fallback when the version cannot be read.
func TestZCodeDesktopVersionSelectsTheLiveConfig(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(os.Getenv("HOME"), ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No hooks block: only the version can identify this as a live config.
	bare := `{"mcp":{"servers":{"docs":{"command":"docs-server"}}}}`
	if err := os.WriteFile(legacy, []byte(bare), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := zcodeDesktopVersion
	defer func() { zcodeDesktopVersion = orig }()

	zcodeDesktopVersion = func() ([3]int, bool) { return [3]int{3, 17, 2}, true }
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(legacy)
	if !strings.Contains(string(b), "hook-context") {
		t.Errorf("3.17+ did not get hooks in config.json: %s", b)
	}
	if strings.Contains(string(b), `"deja"`) {
		t.Errorf("config.json gained a server, which the migration would carry back: %s", b)
	}

	// A fresh 3.17+ install before config.json even exists must also recognise
	// config.json as the live runtime config.
	homeFresh := t.TempDir()
	t.Setenv("HOME", homeFresh)
	t.Setenv("USERPROFILE", homeFresh)
	if !zcodeConfigIsLiveRuntimeConfig() {
		t.Error("3.17+ without config.json was not recognised as live runtime config")
	}

	// An older runtime keeps the setting.json behaviour: config.json is a
	// first-launch migration source and must stay byte for byte.
	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	t.Setenv("USERPROFILE", home2)
	dir2 := filepath.Join(home2, ".zcode", "cli")
	legacy2 := filepath.Join(dir2, "config.json")
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy2, []byte(bare), 0o600); err != nil {
		t.Fatal(err)
	}
	zcodeDesktopVersion = func() ([3]int, bool) { return [3]int{3, 14, 4}, true }
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(legacy2); string(got) != bare {
		t.Errorf("3.14.4 config.json was changed:\n%s", got)
	}

	// Version unreadable: fall back to the file's shape, so a live config with
	// a hooks block is still recognised on another platform.
	home3 := t.TempDir()
	t.Setenv("HOME", home3)
	t.Setenv("USERPROFILE", home3)
	dir3 := filepath.Join(home3, ".zcode", "cli")
	legacy3 := filepath.Join(dir3, "config.json")
	if err := os.MkdirAll(dir3, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"hooks":{"enabled":true,"events":{"PreToolUse":[]}}}`
	if err := os.WriteFile(legacy3, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	zcodeDesktopVersion = func() ([3]int, bool) { return [3]int{}, false }
	if !zcodeConfigIsLiveRuntimeConfig() {
		t.Error("a live config with hooks.events was not recognised without a version")
	}
}

// doctor reports against whichever file exists. A 3.17+ machine has only
// config.json, so asking for setting.json read a wired install as missing.
func TestDoctorZCodeNamesTheFileThatIsThere(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(os.Getenv("HOME"), ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"mcp":{"servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}},"hooks":{"enabled":true,"events":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context --strict","timeout":30}]}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := zcodeDoctorPath(); got != legacy {
		t.Errorf("doctor path = %q, want %q", got, legacy)
	}
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "zcode") {
		t.Errorf("doctor has no zcode row:\n%s", out)
	}
	if strings.Contains(out, "setting.json") {
		t.Errorf("doctor still names setting.json on a config.json machine:\n%s", out)
	}
}
