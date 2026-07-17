package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuidanceTargetsAreUserLevelAndRespectXDG(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got := guidancePath("claude-code"); got != filepath.Join(home, ".claude", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("claude path = %q", got)
	}
	if got := guidancePath("opencode"); got != filepath.Join(xdg, "opencode", "AGENTS.md") {
		t.Fatalf("opencode path = %q", got)
	}
	for _, harness := range []string{"cursor", "grok", "antigravity"} {
		if got := guidancePath(harness); got != "" {
			t.Fatalf("%s path = %q, want unsupported", harness, got)
		}
	}
}

func TestGuidanceAppendPreservesAndRewrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := guidancePath("codex")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Personal rules\n\nkeep this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := installGuidance("codex", false)
	if err != nil || r.Action != "updated" {
		t.Fatalf("install = %#v, %v", r, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "keep this") || strings.Count(string(b), guidanceStart) != 1 {
		t.Fatalf("surrounding content or marker lost: %s", b)
	}
	if r, err = installGuidance("codex", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	if r, err = installGuidance("codex", true); err != nil || r.Action != "updated" {
		t.Fatalf("uninstall = %#v, %v", r, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), guidanceStart) || !strings.Contains(string(b), "keep this") {
		t.Fatalf("uninstall changed user content: %s", b)
	}
}

func TestClaudeGuidanceIsOwnedAndOptOutWorks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := guidancePath("claude-code")
	r, err := installGuidance("claude-code", false)
	if err != nil || r.Action != "created" || r.Path != path {
		t.Fatalf("install = %#v, %v", r, err)
	}
	if got := guidanceStatus("claude-code"); got != "written" {
		t.Fatalf("guidance status = %q", got)
	}
	if got := guidanceOutput("cursor", installResult{}); got != "cursor: guidance unsupported" {
		t.Fatalf("unsupported output = %q", got)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "deja-history") || !strings.Contains(string(b), "recall_context") {
		t.Fatalf("skill content incomplete: %s", b)
	}
	if r, err = installGuidance("claude-code", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	if r, err = installGuidance("claude-code", true); err != nil || r.Action != "removed" {
		t.Fatalf("uninstall = %#v, %v", r, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owned skill still exists, err=%v", err)
	}
}

func TestGuidanceBlockHandlesCRLFAndUnsupportedResult(t *testing.T) {
	old := "# Rules\r\n\r\n" + guidanceStart + "\r\nold\r\n" + guidanceEnd + "\r\n"
	got := updateGuidanceBlock(old, false)
	if strings.Count(got, guidanceStart) != 1 || !strings.Contains(got, "\r\n") || strings.Contains(got, "old") {
		t.Fatalf("CRLF rewrite = %q", got)
	}
	if r, err := guidanceResult("cursor", false); err != nil || r.Path != "" {
		t.Fatalf("unsupported guidance = %#v, %v", r, err)
	}
}

func TestInstallNoGuidanceOptOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if err := runInstall([]string{"codex", "--no-guidance"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("guidance was written despite opt-out, err=%v", err)
	}
}
