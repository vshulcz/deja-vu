package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ZCode keeps the server and the hooks in one file, and the three things that
// make the difference between working and silently doing nothing are: the
// server sits one level deeper than everywhere else (`mcp.servers`), the hooks
// do not run at all without `hooks.enabled`, and the output has to carry no
// key ZCode does not know (#3651).
func TestInstallZCodeWritesTheServerAndTheHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".zcode", "cli", "config.json")

	// Something of the user's is already in the file: it has to survive both
	// halves of the install.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"glm-5.2","mcp":{"servers":{"theirs":{"command":"x"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installZCode("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}

	cfg := readZCodeConfig(t, path)
	if cfg.Model != "glm-5.2" {
		t.Errorf("the install rewrote the user's own settings: model = %q", cfg.Model)
	}
	if _, ok := cfg.MCP.Servers["theirs"]; !ok {
		t.Error("another server was dropped from the map")
	}
	srv, ok := cfg.MCP.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server under mcp.servers: %v", cfg.MCP.Servers)
	}
	if len(srv.Args) == 0 || srv.Args[len(srv.Args)-1] != "mcp" {
		t.Errorf("server entry = %q %v, want it to end in `mcp`", srv.Command, srv.Args)
	}
	if !cfg.Hooks.Enabled {
		t.Error("hooks.enabled is false, so the hooks that were just written never fire")
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		entries := cfg.Hooks.Events[event]
		if len(entries) == 0 {
			t.Fatalf("%s has no entry", event)
		}
		cmd := entries[0].Hooks[0].Command
		if !strings.Contains(cmd, "deja") {
			t.Errorf("%s runs %q", event, cmd)
		}
		// Without --strict the receipt line rides along, ZCode rejects the
		// whole response over it, and the context never arrives.
		if !strings.Contains(cmd, "--strict") {
			t.Errorf("%s = %q, want --strict so the output passes ZCode's schema", event, cmd)
		}
		if entries[0].Hooks[0].Type != "command" {
			t.Errorf("%s type = %q, want command", event, entries[0].Hooks[0].Type)
		}
	}

	// Twice is once: a second install must not stack a second copy.
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	again := readZCodeConfig(t, path)
	if n := len(again.Hooks.Events["SessionStart"]); n != 1 {
		t.Errorf("SessionStart has %d entries after two installs, want 1", n)
	}

	// And out again, leaving what was not ours.
	if _, err := installZCodeHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCode("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	out := readZCodeConfig(t, path)
	if len(out.Hooks.Events["SessionStart"]) != 0 {
		t.Errorf("a hook of ours survived uninstall: %+v", out.Hooks.Events["SessionStart"])
	}
	if _, ok := out.MCP.Servers["deja"]; ok {
		t.Error("the server survived uninstall")
	}
	if _, ok := out.MCP.Servers["theirs"]; !ok {
		t.Error("uninstall took somebody else's server with it")
	}
}

// The strict shape is the point of the flag: one unknown key and ZCode discards
// the whole response, so the receipt has to go and the context has to stay.
func TestStrictHookOutputDropsTheReceiptOnly(t *testing.T) {
	var resp sessionStartHookResponse
	resp.SystemMessage = "deja: 12 sessions indexed"
	resp.HookSpecificOutput.HookEventName = "SessionStart"
	resp.HookSpecificOutput.AdditionalContext = "what you fixed last week"

	loose := marshalHookResponse(t, resp, false)
	if _, ok := loose["systemMessage"]; !ok {
		t.Error("the receipt is gone where the host can take it")
	}
	strict := marshalHookResponse(t, resp, true)
	if _, ok := strict["systemMessage"]; ok {
		t.Error("the receipt survived the strict shape, so ZCode would discard the context with it")
	}
	out, _ := strict["hookSpecificOutput"].(map[string]any)
	if out["additionalContext"] != "what you fixed last week" {
		t.Errorf("the context did not survive: %v", strict)
	}
	if len(strict) != 1 {
		t.Errorf("the strict shape carries %d keys, want only hookSpecificOutput: %v", len(strict), strict)
	}
}

func marshalHookResponse(t *testing.T, resp sessionStartHookResponse, strict bool) map[string]any {
	t.Helper()
	was := strictHookOutput
	strictHookOutput = strict
	defer func() { strictHookOutput = was }()
	if strict {
		resp.SystemMessage = ""
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

type zcodeConfig struct {
	Model string `json:"model"`
	MCP   struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"servers"`
	} `json:"mcp"`
	Hooks struct {
		Enabled bool `json:"enabled"`
		Events  map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"-"`
		Raw map[string]json.RawMessage `json:"-"`
	} `json:"hooks"`
}

// readZCodeConfig reads the file back the way ZCode does, with the events
// wherever they are: deja writes them at the top of `hooks` on a fresh file and
// under `hooks.events` when something already put them there.
func readZCodeConfig(t *testing.T, path string) zcodeConfig {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg zcodeConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("the config is not readable JSON: %v", err)
	}
	var whole struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(b, &whole); err != nil {
		t.Fatal(err)
	}
	cfg.Hooks.Events = map[string][]struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	}{}
	source := whole.Hooks
	if nested, ok := whole.Hooks["events"]; ok {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(nested, &inner); err == nil {
			source = inner
		}
	}
	for key, raw := range source {
		if key == "enabled" || key == "events" {
			continue
		}
		var entries []struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			continue
		}
		cfg.Hooks.Events[key] = entries
	}
	return cfg
}
