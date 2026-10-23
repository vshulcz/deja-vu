package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Claude Code has no per-entry switch: 2.1.302 starts an mcpServers entry
// carrying `"disabled": true` and deja answers in every session. Install and
// doctor both called it switched off, the Kiro/Cursor reading of the key
// (#4303), so the person who wrote it was told recall was off while it ran.
// Claude's own switch is disabledMcpServers, which doctor reads (#4468).
func TestClaudeEntryDisabledFlagIsNotASwitch(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := sources.ClaudeJSONPath()
	body := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"],"disabled":true}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := installClaude(bin, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Note, "switched off") {
		t.Errorf("install note = %q, but Claude Code runs this entry", res.Note)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"disabled": true`) && !strings.Contains(string(b), `"disabled":true`) {
		t.Errorf("install dropped the person's own key:\n%s", b)
	}

	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		MCP []doctorMCPStatus `json:"mcp"`
	}
	if uerr := json.Unmarshal([]byte(out), &report); uerr != nil {
		t.Fatalf("doctor --json: %v: %s", uerr, out)
	}
	seen := false
	for _, r := range report.MCP {
		if r.Name != "claude-code" {
			continue
		}
		seen = true
		if r.State != "wired" || r.SwitchedOff {
			t.Errorf("claude-code row = %+v, want wired and not switched_off", r)
		}
	}
	if !seen {
		t.Fatalf("no claude-code row:\n%s", out)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "the entry is switched off — claude-code") {
		t.Errorf("the text report calls the entry off:\n%s", text)
	}

	// A ~/.claude.json with a trailing comma goes through the JSONC writer.
	jsonc := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"],"disabled":true,},}}`
	if werr := os.WriteFile(cfg, []byte(jsonc), 0o644); werr != nil {
		t.Fatal(werr)
	}
	res, err = installClaude(bin, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Note, "switched off") {
		t.Errorf("jsonc install note = %q, but Claude Code runs this entry", res.Note)
	}

	// The same key still switches Kiro off.
	kiro := kiroMCPSettingsPath()
	if err := os.MkdirAll(filepath.Dir(kiro), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kiro, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dejaEntrySwitchedOff("kiro", kiro) {
		t.Error("kiro: a disabled entry no longer reads as switched off")
	}
}
