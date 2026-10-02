package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zcodeDefaultSetting is the setting.json the ZCode 3.14.4 runtime writes on
// its first launch (setting.example.json in zcode-app-cli 0.16.9): the server
// map under mcp.servers, and the hooks off, with every event listed under
// hooks.events.
const zcodeDefaultSetting = `{
  "modelStream": {
    "idleTimeoutMs": 60000
  },
  "permission": {
    "mode": "build",
    "allowedTools": [],
    "disallowedTools": [],
    "autoApproveHighRisk": false,
    "allowMediumRiskInAuto": false
  },
  "storage": {
    "dir": "~/.zcode",
    "sessionDbPath": "~/.zcode/cli/db/db.sqlite"
  },
  "network": {
    "timeout": 180000
  },
  "features": {
    "compact": true,
    "rewind": true,
    "subagent": true,
    "skill": true,
    "mcp": true
  },
  "subagents": {
    "autoBackgroundMs": 1000
  },
  "memory": {
    "autoConsolidate": true,
    "summaryMaxBytes": 8192
  },
  "mcp": {
    "servers": {}
  },
  "plugins": {
    "enabled": true,
    "dirs": [],
    "enabledPlugins": {},
    "options": {},
    "suppressedBuiltins": []
  },
  "skills": {
    "enabled": true,
    "includeInstructions": true,
    "metadataBudget": 20000,
    "roots": []
  },
  "skill": {},
  "command": {},
  "logging": {
    "level": "info",
    "format": "text"
  },
  "ui": {
    "theme": "auto",
    "tuiMode": "regular",
    "copyOnSelect": true,
    "notifications": {
      "method": "auto",
      "condition": "unfocused"
    }
  },
  "toolConcurrency": {
    "maxConcurrency": 10
  },
  "modelAnomalyGuard": {
    "repeatedToolCallWarningThreshold": 3,
    "maxBudgetWarningsPerTurn": 3
  },
  "hooks": {
    "enabled": false,
    "timeoutMs": 60000,
    "maxOutputBytes": 32768,
    "events": {
      "SessionStart": [],
      "UserPromptSubmit": [],
      "PreToolUse": [],
      "PermissionRequest": [],
      "PostToolUse": [],
      "PostToolUseFailure": [],
      "Stop": []
    }
  }
}
`

// The runtime reads its servers and hooks from ~/.zcode/cli/setting.json, the
// hooks under hooks.events. config.json is read once, as the source of a
// first-launch migration, so on a machine where ZCode had run deja's install
// reached nothing and doctor still said wired (#4429).
func TestInstallZCodeWritesTheRuntimeSettingFile(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setting, []byte(zcodeDefaultSetting), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "zcode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Errorf("install wrote %s, which the runtime does not load", legacy)
	}
	var cfg struct {
		MCP struct {
			Servers map[string]json.RawMessage `json:"servers"`
		} `json:"mcp"`
		Hooks struct {
			Enabled bool                       `json:"enabled"`
			Events  map[string]json.RawMessage `json:"events"`
			Rest    map[string]json.RawMessage `json:"-"`
		} `json:"hooks"`
	}
	b, err := os.ReadFile(setting)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("setting.json is not JSON after install: %v", err)
	}
	if _, ok := cfg.MCP.Servers["deja"]; !ok {
		t.Errorf("no deja server under mcp.servers in setting.json: %s", b)
	}
	if !cfg.Hooks.Enabled {
		t.Errorf("hooks.enabled is off, so the hooks never run: %s", b)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if !strings.Contains(string(cfg.Hooks.Events[event]), "--strict") {
			t.Errorf("hooks.events.%s = %s, want deja's hook", event, cfg.Hooks.Events[event])
		}
	}
	var top map[string]map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err == nil {
		if _, ok := top["hooks"]["SessionStart"]; ok {
			t.Errorf("a hook directly under hooks, where the runtime does not look: %s", b)
		}
	}
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "setting.json") {
		t.Errorf("doctor does not name setting.json:\n%s", out)
	}
}

// An install from before this fix left deja's entries in config.json. The
// uninstall takes them out of there too, so a later first-launch migration
// cannot bring back a server deja was asked to remove.
func TestUninstallZCodeClearsTheOldConfigFile(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"hooks":{"SessionStart":[{"hooks":[{"command":"/home/u/.config/deja/bin/deja-hook hook-context --strict","timeout":30,"type":"command"}]}],"PostToolUse":[{"hooks":[{"command":"/tmp/notify.sh","timeout":5,"type":"command"}]}],"enabled":true},"mcp":{"servers":{"deja":{"args":["mcp"],"command":"/usr/local/bin/deja","type":"stdio"},"docs":{"command":"docs-server"}}}}`
	if err := os.WriteFile(legacy, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "zcode-auto"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("the reader's config.json is gone: %v", err)
	}
	if strings.Contains(string(b), "deja") {
		t.Errorf("deja's entries survived in config.json: %s", b)
	}
	if !strings.Contains(string(b), "notify.sh") || !strings.Contains(string(b), "docs-server") {
		t.Errorf("uninstall took the reader's own entries: %s", b)
	}
}

// ZCode runs config-file hooks only with hooks.enabled on, and its own
// setting.json starts with it off. Install turned it on and uninstall left it
// on, so hooks the reader had switched off ran after an install and an
// uninstall, and the re-marshalled file came back with its keys sorted (#4431).
func TestUninstallZCodePutsTheHooksSwitchBack(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := strings.Replace(zcodeDefaultSetting, `"PostToolUse": [],`,
		`"PostToolUse": [{"hooks": [{"type": "command", "command": "/tmp/notify.sh", "timeout": 5}]}],`, 1)
	if theirs == zcodeDefaultSetting {
		t.Fatal("the fixture has no PostToolUse list to put the reader's hook in")
	}
	if err := os.WriteFile(setting, []byte(theirs), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "zcode-auto", "--no-index")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out, "hooks.enabled") {
		t.Errorf("install turned on hooks the reader had off and did not say so:\n%s", out)
	}
	if _, err := captureRun(t, "uninstall", "zcode-auto"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, err := os.ReadFile(setting)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != theirs {
		t.Errorf("setting.json did not come back as it was.\nbefore:\n%s\nafter:\n%s", theirs, got)
	}

	// A switch that was already on stays on: something else may run on it.
	on := strings.Replace(theirs, `"enabled": false,
    "timeoutMs"`, `"enabled": true,
    "timeoutMs"`, 1)
	if on == theirs {
		t.Fatal("the fixture has no hooks.enabled to turn on")
	}
	if err := os.WriteFile(setting, []byte(on), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "zcode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "zcode-auto"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if got, _ := os.ReadFile(setting); string(got) != on {
		t.Errorf("uninstall changed a file whose switch was already on:\n%s", got)
	}
}

// Before ZCode's first launch, setting.json is not there and the runtime will
// build it from config.json, minus the provider fields, but only while
// setting.json is missing. An install that created setting.json first took
// that migration away: the reader's servers, permissions and hooks in
// config.json never reached the runtime. Install starts the file the way the
// runtime would, and only when the runtime has not already done so (#4429).
func TestInstallZCodeBeforeFirstLaunchKeepsTheMigration(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{
    "provider": {"glm": {"apiKey": "sk-test"}},
    "model": "glm/glm-5",
    "permission": {"mode": "plan"},
    "mcp": {"servers": {"docs": {"type": "stdio", "command": "docs-server"}}}
}
`
	if err := os.WriteFile(legacy, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "zcode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(setting)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"docs"`, `"plan"`, `"deja"`, "hook-context"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("setting.json lacks %s, which the first-launch migration would have carried:\n%s", want, b)
		}
	}
	for _, not := range []string{"sk-test", "glm-5"} {
		if strings.Contains(string(b), not) {
			t.Errorf("setting.json carries %s, a provider field the runtime leaves in config.json:\n%s", not, b)
		}
	}
	if got, _ := os.ReadFile(legacy); string(got) != old {
		t.Errorf("install changed config.json:\n%s", got)
	}

	// Once the runtime has migrated, its marker says so, and config.json is
	// never read again: a setting.json made then starts empty.
	if err := os.Remove(setting); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "migrations", "settings-v1.json"), []byte(`{"schemaVersion":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "zcode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(setting); strings.Contains(string(b), "docs-server") {
		t.Errorf("setting.json took config.json after the runtime's own migration had run:\n%s", b)
	}
}

// ZCode's first-launch migration copies config.json across as it is, so a
// machine wired by an older deja has its hooks directly under hooks, where the
// runtime does not look, and a reader can switch hooks.enabled off. Either way
// no hook runs, and the row said wired because the file names hook-context
// (#4429).
func TestDoctorZCodeHooksThatDoNotRunAreStale(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := `[{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context --strict","timeout":30}]}]`
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "zcode" {
			row = a
		}
	}
	for name, body := range map[string]string{
		"flat":     `{"hooks":{"enabled":true,"SessionStart":` + hook + `}}`,
		"disabled": `{"hooks":{"enabled":false,"events":{"SessionStart":` + hook + `}}}`,
	} {
		if err := os.WriteFile(setting, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if state, _ := autoWiringState(row); state != "stale" {
			t.Errorf("%s: state = %q, want stale", name, state)
		}
	}
	// The control: the shape the runtime runs.
	if err := os.WriteFile(setting, []byte(`{"hooks":{"enabled":true,"events":{"SessionStart":`+hook+`}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, _ := autoWiringState(row); state != "wired" {
		t.Errorf("runtime shape: state = %q, want wired", state)
	}
}
