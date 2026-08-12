package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
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
	// Inside the plugin: antigravity ingests skills/ from a directory marked by
	// plugin.json, and `agy plugin validate` confirms it there. Beside the
	// plugin nothing reads it.
	if got := guidancePath("antigravity"); got != filepath.Join(home, ".gemini", "config", "plugins", "deja", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("antigravity path = %q", got)
	}
	if got := guidancePath("qwen"); got != filepath.Join(home, ".qwen", "QWEN.md") {
		t.Fatalf("qwen path = %q", got)
	}
	if got := guidancePath("copilot"); got != filepath.Join(home, ".copilot", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("copilot path = %q", got)
	}
	// grok reads <cwd>/.grok/GROK.md first, so the home copy never shadows a
	// project's own instructions.
	if got := guidancePath("grok"); got != filepath.Join(home, ".grok", "GROK.md") {
		t.Fatalf("grok path = %q", got)
	}
	// Cursor has no user-level instructions file, which is why it went without
	// guidance for so long. It does read user-level skills.
	if got := guidancePath("cursor"); got != filepath.Join(home, ".cursor", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("cursor path = %q", got)
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
	// goose has no guidance location at all, so it stands in for the
	// unsupported branch that cursor used to occupy.
	if r, err := guidanceResult("goose", false); err != nil || r.Path != "" {
		t.Fatalf("unsupported guidance = %#v, %v", r, err)
	}
}

func TestInstallNoGuidanceOptOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if err := runInstall(index.DefaultDir(), []string{"codex", "--no-guidance"}, false); err != nil {
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

func TestCopilotInstallWritesMCPConfig(t *testing.T) {
	tmp := hermeticEnv(t)
	result, err := installTarget("copilot", "/bin/deja", false)
	if err != nil || result.Action != "created" {
		t.Fatalf("copilot MCP install = %#v, %v", result, err)
	}
	b, err := os.ReadFile(filepath.Join(tmp, "home", ".copilot", "mcp-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"deja"`, `"type": "local"`, `"tools"`, `"mcp"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("mcp-config missing %s: %s", want, b)
		}
	}
	// Uninstall removes only our entry.
	if r, err := installTarget("copilot", "/bin/deja", true); err != nil || r.Action == "" {
		t.Fatalf("copilot uninstall = %#v, %v", r, err)
	}
	b, _ = os.ReadFile(filepath.Join(tmp, "home", ".copilot", "mcp-config.json"))
	if strings.Contains(string(b), `"deja"`) {
		t.Fatalf("deja entry not removed: %s", b)
	}
}

func TestInstallGuidanceSkillErrorBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	// path == "" branch for a harness without a guidance location.
	if r, err := installGuidance("goose", false); err != nil || r.Path != "" {
		t.Fatalf("goose guidance = %#v err=%v", r, err)
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
	// Windows maps the squatted read to not-exist, which reads as unchanged.
	if runtime.GOOS != "windows" {
		if _, err := installGuidance("copilot", true); err == nil {
			t.Fatal("expected copilot uninstall read error")
		}
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

// Guidance is "written" when deja's block is in the file, not when the file
// exists. Anyone keeping their own AGENTS.md was told deja had written
// guidance there on a machine where install had never run (#637).
func TestGuidanceStatusReadsTheMarkerNotTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "AGENTS.md")

	if got := guidanceStatus("codex"); got != "missing" {
		t.Fatalf("no file at all: got %q", got)
	}
	if err := os.WriteFile(path, []byte("# my own agents file\n\nnothing from deja\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("codex"); got != "absent" {
		t.Fatalf("someone else's file: got %q, want absent", got)
	}
	if err := os.WriteFile(path, []byte("# mine\n\n"+guidanceStart+"\nbody\n"+guidanceEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("codex"); got != "written" {
		t.Fatalf("our block is in it: got %q", got)
	}
}

// A skill file is deja's own, written whole, and carries no marker — so the
// marker check must not apply to it. This is a guard for the carve-out rather
// than a test of the #637 fix: it passes against the old stat-only behaviour
// too, and it exists so that behaviour cannot be restored by accident when
// the marker check moves.
func TestGuidanceStatusForAFileDejaOwnsWhole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	skill := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("claude-code"); got != "missing" {
		t.Fatalf("no skill file: got %q", got)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: deja-history\n---\n\nbody with no marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("claude-code"); got != "written" {
		t.Fatalf("a skill file deja wrote whole: got %q, want written", got)
	}
	// An empty file is an interrupted write, not guidance — the one shape
	// where existence still fails to mean "we wrote it" in a directory deja
	// owns.
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("claude-code"); got != "absent" {
		t.Fatalf("an empty skill file: got %q, want absent", got)
	}
}

// Every harness whose guidance is a file deja writes whole, so none of them
// can be dropped from the helper unnoticed — and the helper must name exactly
// the set the install path branches on, or doctor starts lying again.
func TestGuidanceOwnsWholeFileMatchesTheInstallPath(t *testing.T) {
	for _, h := range []string{"claude-code", "claude", "antigravity", "copilot", "pi"} {
		if !guidanceOwnsWholeFile(h) {
			t.Errorf("%q is written whole by installGuidance", h)
		}
		// guidanceText branches on the same helper: a whole-file harness gets
		// skill frontmatter, never the marker pair.
		if txt := guidanceText(h); strings.Contains(txt, guidanceStart) {
			t.Errorf("%q got marker text for a file deja owns whole", h)
		}
	}
	for _, h := range []string{"codex", "opencode", "gemini", "qwen", "kimi", "grok"} {
		if guidanceOwnsWholeFile(h) {
			t.Errorf("%q shares its file with the user", h)
		}
		if txt := guidanceText(h); !strings.Contains(txt, guidanceStart) {
			t.Errorf("%q must get a marked block, not a whole file", h)
		}
	}
}

// The path lookup took the raw name while the whole-file check took the
// normalised one, so the function contradicted itself about what it accepts.
func TestGuidanceStatusAcceptsAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, alias := range []string{"opencode-auto", "codex-auto", "claude-auto"} {
		if got := guidanceStatus(alias); got == "unsupported" {
			t.Errorf("%q resolves to a real harness but reported %q", alias, got)
		}
	}
}
