package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A machine that upgraded is not in the healthy state: the entries were written
// by the version before, so the row comes out stale, or untrusted — and the note
// naming the binary that is gone printed only under a row that came out wired.
// The rows most likely to be pointing at nothing were the ones saying nothing
// about it (#3502).
func TestDoctorNamesADeadBinaryUnderAStaleRow(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "Cellar", "deja-vu", "0.19.3", "bin", "deja")

	// Qwen's row looks for `hook-prompt`. This file has deja's own binary in it
	// and an older event, so the row reads stale — the state a settings.json
	// written by an earlier version lands in.
	qwen := filepath.Join(sources.QwenConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(qwen), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(qwen, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorAutoRecall(&out)
	got := out.String()
	if !strings.Contains(got, "stale") {
		t.Fatalf("the fixture did not produce a stale row, so this measures nothing:\n%s", got)
	}
	if !strings.Contains(got, "runs "+gone+", which is not there") {
		t.Errorf("a stale row said nothing about the binary it names being gone:\n%s", got)
	}
}

// Codex's own row has a third state: trusted, disabled, or never shown to codex
// at all. An upgraded machine lands in the last one, and that was the only thing
// the row said.
func TestDoctorNamesADeadBinaryUnderAnUntrustedCodexRow(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "Cellar", "deja-vu", "0.19.3", "bin", "deja")

	home := sources.CodexHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	// A config with no trust entry for the hook: codex has never been shown it.
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[mcp_servers.deja]\ncommand = \""+gone+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorCodexHook(&out)
	got := out.String()
	if !strings.Contains(got, "untrusted") {
		t.Fatalf("the fixture did not produce an untrusted row, so this measures nothing:\n%s", got)
	}
	if !strings.Contains(got, "runs "+gone+", which is not there") {
		t.Errorf("an untrusted row said nothing about the binary it names being gone:\n%s", got)
	}
}
