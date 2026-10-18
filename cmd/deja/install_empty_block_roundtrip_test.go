package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Uninstall gives back the block deja wrote into as it found it: a block the
// reader had, empty, stays, and one deja added to an empty config goes. The
// hook writers, zed, VS Code, grok, prime and amp deleted the reader's, and
// opencode and kilo kept their own (#4562).
func TestUninstallGivesBackTheEmptyBlock(t *testing.T) {
	for _, c := range []struct {
		target, seed string
		path         func() string
	}{
		{"claude-auto", "{\n  \"hooks\": {}\n}\n", func() string { return filepath.Join(sources.ClaudeConfigDir(), "settings.json") }},
		{"codex-auto", "{\n  \"hooks\": {}\n}\n", func() string { return filepath.Join(sources.CodexHome(), "hooks.json") }},
		{"cursor-auto", "{\n  \"hooks\": {}\n}\n", func() string { return filepath.Join(sources.CursorCLIHome(), "hooks.json") }},
		{"commandcode-auto", "{\n  \"hooks\": {}\n}\n", commandCodeSettings},
		{"zed", "{\n  \"context_servers\": {}\n}\n", sources.ZedSettingsPath},
		{"vscode", "{\n  \"servers\": {}\n}\n", func() string { return filepath.Join(vsCodeDefaultUserDir(), "mcp.json") }},
		{"grok", "{\n  \"mcp\": {\"servers\": []}\n}\n", func() string { return filepath.Join(sources.GrokHome(), "user-settings.json") }},
		{"prime", "{\n  \"mcpServers\": {}\n}\n", primeSettingsPath},
		{"amp", "{\n  \"amp.mcpServers\": {}\n}\n", sources.AmpSettingsFile},
		{"opencode", "{}\n", func() string { return filepath.Join(opencodeConfigHome(), "opencode", "opencode.json") }},
		{"kilocode", "{}\n", func() string { return filepath.Join(opencodeConfigHome(), "kilo", "kilo.json") }},
		// OpenClaw's config had no mcp key and kept `"mcp": {"servers": {}}`.
		{"openclaw-auto", "{\n  \"agents\": {\"defaults\": {\"model\": \"x\"}}\n}\n", openclawConfigPath},
		{"openclaw-auto", "// mine\n{\n  \"agents\": {\"defaults\": {\"model\": \"x\"}}\n}\n", openclawConfigPath},
		{"openclaw-auto", "{}\n", openclawConfigPath},
		{"openclaw", "{\n  \"agents\": {\"defaults\": {\"model\": \"x\"}}\n}\n", openclawConfigPath},
		// Hooks, server and status line go into one settings.json in three
		// writes, so the write that emptied the hooks block was not the last,
		// and by the last one the block was already gone from the file.
		{"qwen-auto", "{\n  \"hooks\": {}\n}\n", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }},
		{"qwen-auto", "{\n  \"$version\": 4,\n  \"hooks\": {},\n  \"ui\": {}\n}\n", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }},
		{"codebuddy-auto", "{\n  \"hooks\": {}\n}\n", codeBuddySettingsPath},
		{"openclaw-auto", "{\n  \"plugins\": {}\n}\n", openclawConfigPath},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); string(got) == c.seed {
				t.Fatalf("install did not write %s", p)
			}
			if _, err := captureRun(t, "uninstall", c.target); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); string(got) != c.seed {
				t.Errorf("uninstall gave back\n%s\nwant\n%s", got, c.seed)
			}
		})
	}
}

// The snapshot settles only what deja did. An empty block the reader added
// after install, or one they took out, is their change, and giving the
// snapshot back undid it (review of #4562).
func TestUninstallKeepsAnEmptyBlockChangedSinceInstall(t *testing.T) {
	for _, c := range []struct {
		name, seed string
		edit       func(map[string]any)
		has, lacks string
	}{
		{"added", "{\n  \"servers\": {}\n}\n", func(m map[string]any) { m["inputs"] = []any{} }, `"inputs"`, ""},
		{"removed", "{\n  \"servers\": {},\n  \"inputs\": []\n}\n", func(m map[string]any) { delete(m, "inputs") }, "", `"inputs"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			p := filepath.Join(vsCodeDefaultUserDir(), "mcp.json")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "vscode", "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(p)
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			c.edit(m)
			b, _ = json.MarshalIndent(m, "", "  ")
			if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "uninstall", "vscode"); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			if c.has != "" && !strings.Contains(string(got), c.has) {
				t.Errorf("the %s the reader added is gone:\n%s", c.has, got)
			}
			if c.lacks != "" && strings.Contains(string(got), c.lacks) {
				t.Errorf("the %s the reader removed is back:\n%s", c.lacks, got)
			}
			if strings.Contains(string(got), "deja") {
				t.Errorf("deja is still wired:\n%s", got)
			}
		})
	}
}

// An empty block a client saves while the uninstall edits the file is the
// client's: the retry edits its version, and measuring that against the file
// as the run first found it gave the snapshot back over it (review of #5184).
func TestUninstallKeepsAnEmptyBlockAClientSavedMidRun(t *testing.T) {
	for _, c := range []struct {
		target string
		path   func() string
	}{
		{"qwen-auto", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }},
		{"codebuddy-auto", codeBuddySettingsPath},
		{"claude-auto", func() string { return filepath.Join(sources.ClaudeConfigDir(), "settings.json") }},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			seed := "{\n  \"hooks\": {}\n}\n"
			if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			saved := false
			beforeConfigReplace = func(path string) {
				if saved || filepath.Base(path) != filepath.Base(p) {
					return
				}
				saved = true
				b, _ := os.ReadFile(p)
				s := strings.Replace(string(b), "{", "{\n  \"clientBlock\": {},", 1)
				_ = os.WriteFile(p+".tmp", []byte(s), 0o644)
				_ = os.Rename(p+".tmp", p)
			}
			t.Cleanup(func() { beforeConfigReplace = func(string) {} })
			if _, err := captureRun(t, "uninstall", c.target); err != nil {
				t.Fatal(err)
			}
			if !saved {
				t.Fatal("the client save never ran")
			}
			got, _ := os.ReadFile(p)
			if !strings.Contains(string(got), "clientBlock") {
				t.Errorf("the block the client saved is gone:\n%s", got)
			}
		})
	}
}
