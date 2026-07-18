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
	if got := guidancePath("antigravity"); got != filepath.Join(home, ".gemini", "config", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("antigravity path = %q", got)
	}
	if got := guidancePath("qwen"); got != filepath.Join(home, ".qwen", "QWEN.md") {
		t.Fatalf("qwen path = %q", got)
	}
	if got := guidancePath("copilot"); got != filepath.Join(home, ".copilot", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("copilot path = %q", got)
	}
	for _, harness := range []string{"cursor", "grok"} {
		if got := guidancePath(harness); got != "" {
			t.Fatalf("%s path = %q, want unsupported", harness, got)
		}
	}
}

func TestOwnedGuidanceTargetsAndMarkerBoundaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, harness := range []string{"antigravity", "qwen", "copilot"} {
		r, err := installGuidance(harness, false)
		if err != nil || r.Action != "created" {
			t.Fatalf("%s install = %#v, %v", harness, r, err)
		}
		if r, err = installGuidance(harness, false); err != nil || r.Action != "unchanged" {
			t.Fatalf("%s rerun = %#v, %v", harness, r, err)
		}
		b, _ := os.ReadFile(guidancePath(harness))
		if harness != "qwen" && !strings.Contains(string(b), "name: deja-history") {
			t.Fatalf("%s frontmatter missing: %s", harness, b)
		}
	}
	old := "prose " + guidanceStart + "\nkeep\n" + guidanceEnd + "\n"
	want := old + "\n" + guidanceStart + "\n" + guidanceBody + "\n" + guidanceEnd + "\n"
	if got := updateGuidanceBlock(old, false); got != want {
		t.Fatalf("inline markers were replaced: %q", got)
	}
	qwen := guidancePath("qwen")
	if err := os.WriteFile(qwen, []byte("# Personal context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("qwen", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(qwen)
	if !strings.Contains(string(b), "# Personal context") {
		t.Fatalf("qwen context was not preserved: %s", b)
	}
	if _, err := installGuidance("qwen", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(qwen)
	if strings.Contains(string(b), guidanceStart) || !strings.Contains(string(b), "# Personal context") {
		t.Fatalf("qwen uninstall changed personal context: %s", b)
	}

	squat := filepath.Join(home, ".copilot", "skills", "deja-history")
	if err := os.RemoveAll(filepath.Dir(squat)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(squat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(squat, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("copilot", false); err == nil {
		t.Fatal("expected path-squatting error")
	}
	if _, err := writeIfChanged(filepath.Join(squat, "SKILL.md"), nil, []byte("skill")); err == nil {
		t.Fatal("expected atomic write path-squatting error")
	}
	entries, err := os.ReadDir(filepath.Dir(squat))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(squat) {
		t.Fatalf("temporary file left after failed write: %v", entries)
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

func TestInstallGuidanceReadFailureSurfaces(t *testing.T) {
	tmp := hermeticEnv(t)
	// Squat the skill directory path with a regular file so reading the
	// skill file fails with a non-NotExist error.
	skills := filepath.Join(tmp, "home", ".claude", "skills")
	if err := os.MkdirAll(filepath.Dir(skills), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skills, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("claude-code", false); err == nil {
		t.Fatal("expected guidance install error when skills path is a file")
	}
}

func TestInstallGuidanceEdgeBranches(t *testing.T) {
	hermeticEnv(t)
	if r, err := installGuidance("nope", false); err != nil || r.Path != "" {
		t.Fatalf("unknown harness = %#v err=%v", r, err)
	}
	if r, err := installGuidance("claude-code", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("uninstall with no skill = %#v err=%v", r, err)
	}
}

func TestCopilotInstallIsGuidanceOnly(t *testing.T) {
	if result, err := installTarget("copilot", "/bin/deja", false); err != nil || result.Action != "guidance-only" || result.Path != guidancePath("copilot") {
		t.Fatalf("copilot MCP install = %#v, %v", result, err)
	}
}

func TestInstallGuidanceSkillErrorBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	// path == "" branch for a harness without a guidance location.
	if r, err := installGuidance("grok", false); err != nil || r.Path != "" {
		t.Fatalf("grok guidance = %#v err=%v", r, err)
	}
	// Read failure that is not IsNotExist must surface (copilot skill dir
	// squatted by a file).
	squat := filepath.Join(tmp, "home", ".copilot", "skills")
	if err := os.MkdirAll(filepath.Dir(squat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(squat, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("copilot", false); err == nil {
		t.Fatal("expected copilot read error")
	}
	// Uninstall of a skill whose path parent blocks removal errors out.
	if _, err := installGuidance("copilot", true); err == nil {
		t.Fatal("expected copilot uninstall read error")
	}
	// antigravity skill install + uninstall roundtrip.
	if r, err := installGuidance("antigravity", false); err != nil || r.Action != "created" {
		t.Fatalf("antigravity install = %#v err=%v", r, err)
	}
	if r, err := installGuidance("antigravity", true); err != nil || r.Action != "removed" {
		t.Fatalf("antigravity uninstall = %#v err=%v", r, err)
	}
	if r, err := installGuidance("antigravity", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("antigravity re-uninstall = %#v err=%v", r, err)
	}
}
