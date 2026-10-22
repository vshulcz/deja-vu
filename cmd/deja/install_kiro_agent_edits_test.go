package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kiro-cli edits an agent file in place: `kiro-cli mcp add --agent deja`
// writes the server into ~/.kiro/agents/deja.json and normalises the rest of
// the file (shape captured from kiro-cli 2.32.0). A re-install used to write
// deja's agent whole and drop the server; an uninstall deleted it with the
// file.
func TestKiroAutoKeepsWhatKiroAddedToTheDejaAgent(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := kiroAgentPath()
	hook := kiroTestHookExe(t, path)
	entry := func(sub string) string {
		return `[{"command": "` + hook + ` ` + sub + `", "timeout_ms": 10000, "max_output_size": 10240, "cache_ttl_seconds": 0}]`
	}
	edited := `{
  "name": "deja",
  "description": "` + kiroAgentDescription + `",
  "prompt": null,
  "mcpServers": {
    "gh-tools": {"command": "/usr/bin/gh-mcp", "args": ["stdio"], "env": {}}
  },
  "tools": ["*"],
  "toolAliases": {},
  "allowedTools": ["fs_read"],
  "resources": [],
  "hooks": {
    "stop": [{"command": "say done", "timeout_ms": 1000}, {"command": "/usr/bin/time ` + hook + ` hook-session-end", "timeout_ms": 1000}, ` + strings.TrimPrefix(entry("hook-session-end"), "[") + `,
    "agentSpawn": ` + entry("hook-context --plain") + `,
    "postToolUse": ` + entry("hook-tool-after --defer") + `,
    "preToolUse": ` + entry("hook-tool --defer") + `,
    "userPromptSubmit": ` + entry("hook-prompt --plain") + `
  },
  "toolsSettings": {},
  "includeMcpJson": true,
  "model": "claude-sonnet-4"
}
`
	writeFileMkdir(t, path, edited)

	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	agent := kiroTestAgent(t, path)
	servers, _ := agent["mcpServers"].(map[string]any)
	if _, ok := servers["gh-tools"]; !ok {
		t.Errorf("re-install dropped the server kiro-cli added to the agent:\n%s", readFile(t, path))
	}
	if allowed, _ := agent["allowedTools"].([]any); agent["model"] != "claude-sonnet-4" || len(allowed) != 1 {
		t.Errorf("re-install dropped the agent's model or allowedTools:\n%s", readFile(t, path))
	}
	hooks, _ := agent["hooks"].(map[string]any)
	for trigger, sub := range map[string]string{"agentSpawn": "hook-context", "userPromptSubmit": "hook-prompt",
		"preToolUse": "hook-tool", "postToolUse": "hook-tool-after", "stop": "hook-session-end"} {
		if n := kiroTestDejaHooks(hooks[trigger], sub); n != 1 {
			t.Errorf("%s runs deja %s %d times after re-install, want 1", trigger, sub, n)
		}
	}
	if !strings.Contains(readFile(t, path), `"say done"`) || !strings.Contains(readFile(t, path), "/usr/bin/time ") {
		t.Errorf("re-install dropped the reader's own stop hooks:\n%s", readFile(t, path))
	}

	out, err := captureRun(t, "uninstall", "kiro-auto")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("uninstall deleted an agent that holds the reader's server: %v\n%s", err, out)
	}
	if !strings.Contains(string(got), `"gh-tools"`) || !strings.Contains(string(got), `"say done"`) || !strings.Contains(string(got), "/usr/bin/time ") {
		t.Errorf("uninstall took the reader's additions out of the agent:\n%s", got)
	}
	if strings.Contains(string(got), "hook-context") || strings.Count(string(got), "hook-session-end") != 1 {
		t.Errorf("uninstall left deja's hooks in the agent:\n%s", got)
	}
	if !strings.Contains(out, reportPath(path)) {
		t.Errorf("uninstall did not say it kept the agent:\n%s", out)
	}
}

// `kiro-cli agent upgrade` (2.32) rewrites the agent into the format both
// engines load: hooks become a list of {name, trigger, action}, and it adds a
// default permissions rule and its own kiroUpgrade record. A re-install keeps
// that shape; an uninstall of an upgraded file nobody else touched removes it.
func TestKiroAutoFollowsAnUpgradedDejaAgent(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := kiroAgentPath()
	hook := kiroTestHookExe(t, path)
	h := func(trigger, sub string) string {
		return `{"name": "` + trigger + `-0", "trigger": "` + trigger + `", "action": {"type": "command", "command": "` + hook + ` ` + sub + `"}, "timeout": 10}`
	}
	upgraded := `{
  "description": "` + kiroAgentDescription + `",
  "hooks": [
    ` + h("agentSpawn", "hook-context --plain") + `,
    ` + h("postToolUse", "hook-tool-after --defer") + `,
    ` + h("preToolUse", "hook-tool --defer") + `,
    ` + h("stop", "hook-session-end") + `,
    ` + h("userPromptSubmit", "hook-prompt --plain") + `
  ],
  "includeMcpJson": true,
  "name": "deja",
  "tools": ["*"],
  "allowedTools": [],
  "permissions": {"rules": [{"capability": "fs_read", "match": ["./**"], "effect": "allow"}]},
  "kiroUpgrade": {"revision": 2, "fingerprint": "sha256:aa", "source": "sha256:bb"}
}
`
	writeFileMkdir(t, path, upgraded)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	agent := kiroTestAgent(t, path)
	list, ok := agent["hooks"].([]any)
	if !ok {
		t.Fatalf("re-install turned the upgraded hook list back into the old map:\n%s", readFile(t, path))
	}
	if n := kiroTestDejaHooks(list, "hook-context"); n != 1 {
		t.Errorf("agentSpawn runs deja %d times, want 1:\n%s", n, readFile(t, path))
	}
	if _, ok := agent["permissions"]; !ok {
		t.Errorf("re-install dropped the upgraded agent's permissions:\n%s", readFile(t, path))
	}

	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("uninstall left an upgraded agent with nothing of the reader's in it:\n%s", readFile(t, path))
	}
}

// The default V2 engine runs kiro_default when chat.defaultAgent names an
// agent that is not there; kiro-cli 2.32 prints `agent "deja" not found, using
// "kiro_default"`. doctor called that wired.
func TestDoctorKiroDefaultAgentWithoutTheAgentFile(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	writeFileMkdir(t, kiroSettingsPath(), "{\n  \"chat.defaultAgent\": \"deja\"\n}\n")
	if err := os.Remove(kiroAgentPath()); err != nil {
		t.Fatal(err)
	}
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "kiro" {
			row = a
		}
	}
	if state, _ := autoWiringState(row); state == "wired" {
		t.Errorf("doctor says wired while kiro-cli's default agent file is gone")
	}
	// A deja agent of the reader's own, without deja's hooks, runs none either.
	writeFileMkdir(t, kiroAgentPath(), `{"name":"deja","description":"mine","tools":["read"]}`+"\n")
	if state, _ := autoWiringState(row); state == "wired" {
		t.Errorf("doctor says wired while the default agent runs no deja hook")
	}
	// install leaves that agent alone, so the fix doctor names is the rename.
	if !strings.Contains(kiroV2Note(), "rename it") {
		t.Errorf("doctor's fix for the reader's own deja agent: %s", kiroV2Note())
	}
}

func kiroTestAgent(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &m); err != nil {
		t.Fatalf("agent file is not JSON: %v\n%s", err, readFile(t, path))
	}
	return m
}

// kiroTestHookExe is the hook binary the install wrote into the agent.
func kiroTestHookExe(t *testing.T, path string) string {
	t.Helper()
	hooks := typedOrFatal[map[string]any](t, kiroTestAgent(t, path)["hooks"])
	spawn := typedOrFatal[map[string]any](t, typedOrFatal[[]any](t, hooks["agentSpawn"])[0])
	cmd := typedOrFatal[string](t, spawn["command"])
	exe, _, _ := strings.Cut(cmd, " hook-context")
	return filepath.ToSlash(exe)
}

// kiroTestDejaHooks counts entries, of either agent format, that run deja sub.
func kiroTestDejaHooks(v any, sub string) int {
	n := 0
	list, _ := v.([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		cmd, _ := m["command"].(string)
		if a, ok := m["action"].(map[string]any); ok {
			cmd, _ = a["command"].(string)
		}
		if f := strings.Fields(cmd); len(f) > 1 && f[1] == sub {
			n++
		}
	}
	return n
}
