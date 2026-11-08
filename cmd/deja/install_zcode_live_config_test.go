package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These exercise the fallback when a desktop version cannot be read (another
// platform, or a non-standard install): a config.json the runtime maintains
// carries a hooks block, while a first-launch migration source has none. Only
// the live file gets a second copy of the hooks. The 3.17+ version-driven path
// is covered in zcode_version_test.go.
func TestInstallZCodeHooksAlsoReachALiveConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	legacy := filepath.Join(dir, "config.json")

	// The reader's own hooks have to survive the extra write.
	live := `{
  "mcp": {"servers": {"docs": {"type": "stdio", "command": "docs-server"}}},
  "hooks": {
    "enabled": true,
    "events": {
      "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "/tmp/mine.sh", "timeout": 10}]}]
    }
  }
}
`
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}

	// setting.json is still written the way #4429 requires.
	if b, err := os.ReadFile(setting); err != nil {
		t.Fatalf("setting.json was not written: %v", err)
	} else if !strings.Contains(string(b), "hook-context") {
		t.Errorf("setting.json has no deja hook: %s", b)
	}

	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("config.json is gone: %v", err)
	}
	var cfg struct {
		MCP struct {
			Servers map[string]json.RawMessage `json:"servers"`
		} `json:"mcp"`
		Hooks struct {
			Enabled bool                       `json:"enabled"`
			Events  map[string]json.RawMessage `json:"events"`
		} `json:"hooks"`
	}
	if uerr := json.Unmarshal(b, &cfg); uerr != nil {
		t.Fatalf("config.json is not JSON after install: %v", uerr)
	}
	if !cfg.Hooks.Enabled {
		t.Errorf("hooks.enabled is off in config.json: %s", b)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if !strings.Contains(string(cfg.Hooks.Events[event]), "--strict") {
			t.Errorf("config.json hooks.events.%s = %s, want deja's hook", event, cfg.Hooks.Events[event])
		}
	}
	if !strings.Contains(string(cfg.Hooks.Events["PreToolUse"]), "/tmp/mine.sh") {
		t.Errorf("the reader's own PreToolUse hook was lost: %s", cfg.Hooks.Events["PreToolUse"])
	}
	// The server must not be copied into config.json: the first-launch
	// migration would carry a removed one back (#4429).
	if _, ok := cfg.MCP.Servers["deja"]; ok {
		t.Errorf("config.json gained a deja server: %s", b)
	}
	if _, ok := cfg.MCP.Servers["docs"]; !ok {
		t.Errorf("config.json lost the reader's server: %s", b)
	}
}

// config.json with no hooks.events is the one-off migration source of a first
// launch. Writing hooks into it would leave a file the runtime reads once, and
// #4429's test that install leaves it byte for byte must keep holding.
func TestInstallZCodeHooksLeaveTheMigrationSourceAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"provider":{"glm":{"apiKey":"sk-test"}},"model":"glm-5","mcp":{"servers":{"docs":{"command":"docs-server"}}}}`
	if err := os.WriteFile(legacy, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(legacy); string(got) != old {
		t.Errorf("install changed the migration source:\n%s", got)
	}
}

// The extra copy is deja's too, so uninstall takes it back out and leaves the
// reader's own hooks and the file itself as they were.
func TestUninstallZCodeHooksClearsTheLiveConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"hooks":{"enabled":true,"events":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/tmp/mine.sh","timeout":10}]}]}}}`
	if err := os.WriteFile(legacy, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "deja hook-") {
		t.Errorf("uninstall left deja's hooks in config.json: %s", b)
	}
	if string(b) != live {
		t.Errorf("config.json did not come back as it was.\nbefore:\n%s\nafter:\n%s", live, b)
	}
}

// A runtime that already wrote setting.json reads that file, so there is no
// second copy to make.
func TestInstallZCodeHooksLeaveAnExistingSettingAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"hooks":{"enabled":true,"events":{"PreToolUse":[]}}}`
	if err := os.WriteFile(setting, []byte(`{"hooks":{"enabled":false,"events":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(legacy); string(got) != live {
		t.Errorf("config.json was changed even though setting.json exists:\n%s", got)
	}
}

func TestInstallZCodeAutoReachesLiveConfigAcrossReinstalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"hooks":{"enabled":true,"events":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/tmp/mine.sh","timeout":10}]}]}}}`
	if err := os.WriteFile(legacy, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeAuto("/opt/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hook-context --strict") {
		t.Errorf("installZCodeAuto did not wire hooks into live config.json: %s", b)
	}
	if _, uerr := installZCodeAuto("/opt/bin/deja", true); uerr != nil {
		t.Fatal(uerr)
	}
	after, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != live {
		t.Errorf("config.json did not come back as it was after installZCodeAuto uninstall.\nbefore:\n%s\nafter:\n%s", live, after)
	}
}
