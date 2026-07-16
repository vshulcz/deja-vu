package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mcpLine(name, status string) string {
	return fmt.Sprintf("  %-12s %-14s", name, status)
}

func stubLookup(latest string, ok bool) doctorVersionLookup {
	return func() (string, bool) { return latest, ok }
}

func TestDoctorFullReport(t *testing.T) {
	tmp := hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "sess.jsonl"), "sess", []string{
		`{"type":"user","sessionId":"sess","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello doctor"}}`,
	})
	if err := os.WriteFile(os.Getenv("DEJA_OPENCODE_DB"), []byte("sqlite db bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeJSON, []byte(`{"mcpServers":{"deja":{"command":"/bin/deja","args":["mcp"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	old := version
	version = "1.0.0"
	defer func() { version = old }()

	var out bytes.Buffer
	if err := runDoctor(&out, stubLookup("9.9.9", true)); err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Harness stores:", "Tools:", "MCP wiring:", "Index:", "Version:",
		"claude", "opencode", "aider", "gemini", "cursor", "antigravity", "grok",
		"1 file", mcpLine("claude-code", "wired"), "config missing",
		"not built", "current  1.0.0", "latest   v9.9.9", "update available",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("report contains color codes:\n%s", got)
	}
}

func TestDoctorVersionBranches(t *testing.T) {
	cases := []struct {
		current, latest string
		ok              bool
		want            string
	}{
		{"1.0.0", "9.9.9", true, "update available"},
		{"9.9.9", "9.9.9", true, "up to date"},
		{"9.9.9", "1.0.0", true, "ahead of latest release"},
		{"dev", "9.9.9", true, "dev build"},
		{"1.0.0", "", false, "unable to check"},
	}
	for _, tc := range cases {
		old := version
		version = tc.current
		var out bytes.Buffer
		doctorVersion(&out, stubLookup(tc.latest, tc.ok))
		version = old
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("version(%q,%q,%v) = %q want %q", tc.current, tc.latest, tc.ok, out.String(), tc.want)
		}
	}
}

func TestDoctorMCPWiringStates(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// wired via JSON
	writeFileMkdir(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"deja":{}}}`)
	// present but not wired (TOML without our block)
	writeFileMkdir(t, filepath.Join(home, ".codex", "config.toml"), "[cli]\nauto_update = false\n")
	// wired via TOML at the grok root
	writeFileMkdir(t, filepath.Join(os.Getenv("DEJA_GROK_ROOT"), "config.toml"), "[mcp_servers.deja]\ncommand = \"x\"\n")
	// gemini settings.json left absent -> config missing

	var out bytes.Buffer
	doctorMCP(&out)
	got := out.String()
	for _, want := range []string{
		mcpLine("claude-code", "wired"),
		mcpLine("codex", "not wired"),
		mcpLine("grok", "wired"),
		mcpLine("gemini", "config missing"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MCP wiring missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorJSONCWiringFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(path, []byte("{\n  // comment\n  \"mcp\": { \"deja\": {} }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorJSONWired("mcp")(path) {
		t.Fatal("jsonc with deja should read as wired via fallback")
	}
	if doctorJSONWired("mcp")(filepath.Join(dir, "absent.json")) {
		t.Fatal("absent config must not read as wired")
	}
}

func TestDoctorDispatchHermetic(t *testing.T) {
	hermeticEnv(t)
	old := doctorLookup
	doctorLookup = stubLookup("9.9.9", true)
	defer func() { doctorLookup = old }()
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor dispatch err=%v", err)
	}
	if !strings.Contains(out, "Harness stores:") || !strings.Contains(out, "latest   v9.9.9") {
		t.Fatalf("doctor dispatch out=%q", out)
	}
}

func writeFileMkdir(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
