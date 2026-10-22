package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// cursor-agent 2026.09.02 runs an mcp.json entry with "disabled": true — its
// schema drops the key and `cursor-agent mcp list` says ready — so install and
// doctor calling it off told the person recall was off while it ran (#5162).
func TestCursorEntryDisabledKeyIsNotASwitch(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"],"disabled":true}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "cursor", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "switched off") {
		t.Errorf("install called the cursor entry off:\n%s", out)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"disabled": true`) && !strings.Contains(string(b), `"disabled":true`) {
		t.Errorf("the reader's key was dropped:\n%s", b)
	}
	js, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		MCP []doctorMCPStatus `json:"mcp"`
	}
	if uerr := json.Unmarshal([]byte(js), &report); uerr != nil {
		t.Fatalf("doctor --json: %v: %s", uerr, js)
	}
	seen := false
	for _, r := range report.MCP {
		if r.Name != "cursor" {
			continue
		}
		seen = true
		if r.State != "wired" || r.SwitchedOff {
			t.Errorf("cursor row = %+v, want wired and not switched off", r)
		}
	}
	if !seen {
		t.Fatalf("no cursor row:\n%s", js)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "the entry is switched off") {
		t.Errorf("doctor called the cursor entry off:\n%s", text)
	}
}

// cursor-agent reads hooks.json with its comments dropped, so a commented
// file is a working one. Uninstall refused it as broken with nothing of deja's
// in it, and install called it broken too (#5163).
func TestCursorAutoOverACommentedHooksFile(t *testing.T) {
	hermeticEnv(t)
	dir := sources.CursorCLIHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(dir, "hooks.json")
	seed := "// mine\n{\n  /* v1 */\n  \"version\": 1,\n  \"hooks\": {\n    \"stop\": [{\"command\": \"say done\"}]\n  }\n}\n"
	if err := os.WriteFile(hooks, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "uninstall", "cursor-auto"); err != nil {
		t.Fatalf("uninstall refused a commented hooks.json with nothing of deja's in it: %v\n%s", err, out)
	}
	_, err := captureRun(t, "install", "cursor-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "carries comments") {
		t.Fatalf("install err = %v, want the comments refusal", err)
	}
	if b, _ := os.ReadFile(hooks); string(b) != seed {
		t.Errorf("hooks.json changed:\n%s", b)
	}
	// Refused before the MCP half wrote anything (#2748).
	if _, err := os.Stat(filepath.Join(dir, "mcp.json")); err == nil {
		t.Error("mcp.json was written by a refused target")
	}

	// Nor is the reader's own `version` deja's to take on the way out.
	for _, only := range []string{
		"// mine\n{\"version\": 1}\n",
		"// mine\n{\"version\": 1, \"hooks\": {}}\n",
		"// mine\n{\"version\": 1, \"hooks\": {\"sessionStart\": []}}\n",
		"{\"version\": 1, \"hooks\": {\"sessionStart\": {\"command\": \"mine\"}}}\n",
	} {
		if err := os.WriteFile(hooks, []byte(only), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "uninstall", "cursor-auto"); err != nil {
			t.Errorf("uninstall refused %q: %v\n%s", only, err, out)
		}
		if b, _ := os.ReadFile(hooks); string(b) != only {
			t.Errorf("uninstall changed %q to %q", only, b)
		}
	}
}

// The status line file was outside cursor-auto's pre-flight, so a read-only
// one was refused after mcp.json and hooks.json were written (#5164).
func TestCursorAutoRefusesAReadOnlyStatusLineFileBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	hermeticEnv(t)
	cfg := cursorCLIConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("{\n  \"version\": 1\n}\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "install", "cursor-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("install err = %v, want the read-only refusal", err)
	}
	for _, name := range []string{"mcp.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(sources.CursorCLIHome(), name)); err == nil {
			t.Errorf("%s was written by a refused target", name)
		}
	}

	// A status line already deja's needs no write, and is not refused.
	if err := os.Chmod(cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o444); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "cursor-auto", "--no-index"); err != nil {
		t.Errorf("a repeat install refused an unchanged read-only status line file: %v\n%s", err, out)
	}
	// And uninstall still takes the hooks out, refusing only the status line.
	if _, err := captureRun(t, "uninstall", "cursor-auto"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("uninstall err = %v, want the status line refused", err)
	}
	if b, _ := os.ReadFile(filepath.Join(sources.CursorCLIHome(), "hooks.json")); strings.Contains(string(b), "hook-context") {
		t.Errorf("uninstall left deja's hooks in hooks.json:\n%s", b)
	}
}
