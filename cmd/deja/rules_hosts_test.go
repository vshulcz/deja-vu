package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func clearRulesHostEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"DSH_HOME", "REASONIX_HOME", "OPENCLAW_STATE_DIR", "OPENCLAW_WORKSPACE_DIR"} {
		t.Setenv(v, "")
	}
}

// Each host gets the block in the file its stand saw reach the model: dsh's and
// Reasonix's home, Cursor's own rules directory with the frontmatter that makes
// it always apply, and the OpenClaw workspace's AGENTS.md only once OpenClaw
// has seeded it.
func TestRulesSyncWritesTheNewHostsFiles(t *testing.T) {
	home := rulesHome(t, "deepseek", "reasonix", "cursor", "openclaw")
	clearRulesHostEnv(t)
	// Reasonix keeps its home under %APPDATA% on Windows, ~/.reasonix elsewhere.
	rx := sources.ReasonixHome()
	for _, d := range []string{filepath.Join(home, ".dsh"), rx, filepath.Join(home, ".cursor"), filepath.Join(home, ".openclaw", "workspace")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRulesTestFile(t, rulesSourcePath(), "- a rule\n")
	if _, err := rulesOut(t, "sync"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(home, ".dsh", "AGENTS.md"), filepath.Join(rx, "AGENTS.md")} {
		if got := readRulesTestFile(t, p); got != rulesBlock("- a rule") {
			t.Errorf("%s = %q", p, got)
		}
	}
	mdc := filepath.Join(home, ".cursor", "rules", "deja-rules.mdc")
	if got, want := readRulesTestFile(t, mdc), "---\nalwaysApply: true\n---\n\n"+rulesBlock("- a rule"); got != want {
		t.Errorf("cursor rule = %q, want %q", got, want)
	}
	ws := filepath.Join(home, ".openclaw", "workspace", "AGENTS.md")
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Fatalf("deja created the OpenClaw workspace's AGENTS.md, which skips its first-run setup: %v", err)
	}
	writeRulesTestFile(t, ws, "# AGENTS.md\n")
	if _, err := rulesOut(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if got := readRulesTestFile(t, ws); !strings.HasPrefix(got, "# AGENTS.md\n") || !strings.Contains(got, rulesBlock("- a rule")) {
		t.Errorf("workspace AGENTS.md = %q", got)
	}
}

// The workspace is the one OpenClaw itself resolves: its variable, then the
// config, then the state directory.
func TestOpenClawWorkspaceFollowsTheConfig(t *testing.T) {
	rulesHome(t)
	clearRulesHostEnv(t)
	home := homeDir()
	if got, want := openclawWorkspace(), filepath.Join(home, ".openclaw", "workspace"); got != want {
		t.Errorf("default workspace = %q, want %q", got, want)
	}
	writeRulesTestFile(t, openclawConfigPath(), `{
  // the reader's own
  "agents": {"defaults": {"workspace": "~/agent-home"}}
}`)
	if got, want := openclawWorkspace(), filepath.Join(home, "agent-home"); got != want {
		t.Errorf("configured workspace = %q, want %q", got, want)
	}
	t.Setenv("OPENCLAW_WORKSPACE_DIR", "/srv/ws")
	if got := openclawWorkspace(); got != "/srv/ws" {
		t.Errorf("OPENCLAW_WORKSPACE_DIR gave %q", got)
	}
}

// Muse loads the first of its own AGENTS.md, ~/.claude/CLAUDE.md and Codex's
// AGENTS.md. A reader with CLAUDE.md gets the block there, since a new file of
// Muse's own would hide it; with the fallback switched off only Muse's file
// loads.
func TestMuseRulesGoWhereMuseLooks(t *testing.T) {
	rulesHome(t, "muse")
	home := homeDir()
	own := filepath.Join(xdgConfigHome(), "muse", "AGENTS.md")
	if got := museRules().path; got != own {
		t.Errorf("nothing there: %q, want %q", got, own)
	}
	codex := filepath.Join(home, ".codex", "AGENTS.md")
	writeRulesTestFile(t, codex, "codex\n")
	if got := museRules().path; got != codex {
		t.Errorf("only Codex's file: %q", got)
	}
	claude := filepath.Join(home, ".claude", "CLAUDE.md")
	writeRulesTestFile(t, claude, "mine\n")
	if got := museRules().path; got != claude {
		t.Errorf("CLAUDE.md wins over Codex's: %q", got)
	}
	writeRulesTestFile(t, museSettingsPath(), `{"schema_version": 1, "context": {"foreign_personal_rules": false}}`)
	if got := museRules().path; got != own {
		t.Errorf("fallback off: %q, want %q", got, own)
	}
	writeRulesTestFile(t, museSettingsPath(), `{"schema_version": 1}`)
	writeRulesTestFile(t, own, "muse\n")
	if got := museRules().path; got != own {
		t.Errorf("Muse's own file wins: %q", got)
	}
}
