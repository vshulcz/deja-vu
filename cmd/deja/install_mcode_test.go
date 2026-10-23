package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The package MCode loads: a manifest, the hook document, and the icon the
// manifest names. The hook document is the part that matters — the events it
// answers, and the hook each one runs.
func TestMcodeAutoWritesThePackageAndTheMCPEntry(t *testing.T) {
	hermeticEnv(t)
	if _, err := installTarget("mcode-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")

	// The MCP entry, in the shape MCode's own page writes.
	b, err := os.ReadFile(filepath.Join(home, ".minimax", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if jerr := json.Unmarshal(b, &cfg); jerr != nil {
		t.Fatalf("mcp.json is not JSON: %v", jerr)
	}
	s, ok := cfg.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server: %s", b)
	}
	if s.Type != "stdio" {
		t.Errorf("type = %q, want stdio", s.Type)
	}
	if len(s.Args) != 1 || s.Args[0] != "mcp" {
		t.Errorf("args = %v, want [mcp]", s.Args)
	}
	if s.Command == "" {
		t.Error("the server names no command")
	}

	// The manifest, and the icon it points at.
	manifest := filepath.Join(home, ".minimax", "plugins", "deja", ".minimax-plugin", "plugin.json")
	mb, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		SchemaVersion int      `json:"schemaVersion"`
		Name          string   `json:"name"`
		Icon          string   `json:"icon"`
		Hooks         []string `json:"hooks"`
		Skills        []string `json:"skills"`
	}
	if jerr := json.Unmarshal(mb, &m); jerr != nil {
		t.Fatal(jerr)
	}
	if m.SchemaVersion != 1 || m.Name != "deja" {
		t.Errorf("manifest = %s", mb)
	}
	if len(m.Hooks) != 1 || m.Hooks[0] != "hooks/hooks.json" {
		t.Errorf("hooks = %v, want [hooks/hooks.json]", m.Hooks)
	}
	// The skill rides inside the package: MiniMax Code reads no user-level
	// skill directory, so the manifest is what names it.
	if len(m.Skills) != 1 || m.Skills[0] != "skills/deja-history/SKILL.md" {
		t.Errorf("skills = %v, want [skills/deja-history/SKILL.md]", m.Skills)
	}
	skill := filepath.Join(home, ".minimax", "plugins", "deja", "skills", "deja-history", "SKILL.md")
	sb, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("the skill the manifest names is not there: %v", err)
	}
	if !strings.Contains(string(sb), "name: deja-history") {
		t.Errorf("the skill has no frontmatter: %s", sb)
	}
	icon := filepath.Join(home, ".minimax", "plugins", "deja", filepath.FromSlash(m.Icon))
	if fi, err := os.Stat(icon); err != nil || fi.Size() == 0 {
		t.Errorf("the icon the manifest names is not there: %v", err)
	}
}

// Every event the runtime fires has an answer, and the command names the hook
// that answers it: a reader of the file can see which hook runs where.
func TestMcodePluginHooksNameTheHookTheyRun(t *testing.T) {
	hermeticEnv(t)
	if _, err := installTarget("mcode-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("HOME"), ".minimax", "plugins", "deja", "hooks", "hooks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatalf("hooks.json is not JSON: %v", err)
	}
	want := map[string]string{
		"SessionStart":     "hook-context",
		"UserPromptSubmit": "hook-prompt",
		"PreToolUse":       "hook-tool",
		"PostToolUse":      "hook-tool-after",
		"SessionEnd":       "hook-session-end",
		"PreCompact":       "hook-precompact",
	}
	for event, sub := range want {
		groups := file.Hooks[event]
		if len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Errorf("no %s hook in the package", event)
			continue
		}
		h := groups[0].Hooks[0]
		if h.Type != "command" {
			t.Errorf("%s handler type = %q, want command", event, h.Type)
		}
		if !strings.Contains(h.Command, sub) {
			t.Errorf("%s runs %q, want the %s hook", event, h.Command, sub)
		}
		// The runtime allows a handler ten seconds and no more.
		if h.Timeout < 1 || h.Timeout > 10 {
			t.Errorf("%s timeout = %d, want 1..10", event, h.Timeout)
		}
	}
	// The hooks that carry text to the model ask for MiniMax Code's shape.
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		if !strings.Contains(file.Hooks[event][0].Hooks[0].Command, "--mcode") {
			t.Errorf("%s does not ask for MiniMax Code's shape", event)
		}
	}
}

// The answer MCode reads: one JSON object, the text under
// hookSpecificOutput.additionalContext, under the event it answers. A reply
// naming another event is dropped by the runtime, so the name is carried.
func TestMcodeHookShapeIsWhatMiniMaxCodeReads(t *testing.T) {
	var out bytes.Buffer
	emitMcodeContext(&out, "UserPromptSubmit", "déjà vu: the retry count was three")
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("the answer is not JSON: %v: %q", err, out.String())
	}
	if resp.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("event = %q, want the one it answers", resp.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "the retry count was three") {
		t.Errorf("context = %q", resp.HookSpecificOutput.AdditionalContext)
	}
}

// A hook with nothing to say writes nothing: that is how it stays quiet.
func TestMcodeHookShapeSaysNothingWhenThereIsNothing(t *testing.T) {
	var out bytes.Buffer
	emitMcodeContext(&out, "UserPromptSubmit", "   ")
	if out.Len() != 0 {
		t.Errorf("a silent hook wrote %q", out.String())
	}
}

// What the install wrote has to be in the report. The package is three files
// and the MCP entry a fourth, and a report that names one of them leaves the
// rest invisible to whoever reads it (#3254).
func TestMcodeAutoNamesEveryFileItWrote(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "install", "mcode-auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	for _, p := range []string{
		filepath.Join(home, ".minimax", "mcp.json"),
		filepath.Join(home, ".minimax", "plugins", "deja"),
		filepath.Join(home, ".minimax", "plugins", "deja", ".minimax-plugin", "plugin.json"),
		filepath.Join(home, ".minimax", "plugins", "deja", "hooks", "hooks.json"),
		filepath.Join(home, ".minimax", "plugins", "deja", "icon.png"),
		filepath.Join(home, ".minimax", "plugins", "deja", "skills", "deja-history", "SKILL.md"),
	} {
		if !strings.Contains(out, shortHome(p)) {
			t.Errorf("the report does not name %s:\n%s", shortHome(p), out)
		}
	}
}

// `mcode --session <id>` opens a session by the manifest's id (0.6.12
// --help); the registry used to call resume impossible (#5593).
func TestMcodeResumeOpensTheSessionByID(t *testing.T) {
	hermeticEnv(t)
	base := t.TempDir()
	t.Setenv("DEJA_MCODE_ROOT", filepath.Join(base, "v2", "sessions"))
	s := model.Session{Harness: "mcode", ID: "mvs_0f1e2d3c", Project: "p"}
	dir, cmd, err := resumeCommand(s)
	if err != nil || dir != "" || cmd != "mcode --session mvs_0f1e2d3c" {
		t.Fatalf("resume = %q %q %v", dir, cmd, err)
	}

	// The runtime's database records the workspace: the command runs there
	// while it exists, and without the cd once it is gone.
	if _, lerr := exec.LookPath("sqlite3"); lerr != nil {
		t.Skip("sqlite3 not installed")
	}
	ws := filepath.Join(base, "work", "billing-api")
	if merr := os.MkdirAll(ws, 0o755); merr != nil {
		t.Fatal(merr)
	}
	db := sources.McodeStateDB()
	if merr := os.MkdirAll(filepath.Dir(db), 0o755); merr != nil {
		t.Fatal(merr)
	}
	sql := "create table local_runtime_sessions (session_id text primary key, workspace_dir text, project_workspace_dir text, is_default_workspace integer not null default 0);" +
		"insert into local_runtime_sessions values ('mvs_0f1e2d3c', '" + strings.ReplaceAll(ws, "'", "''") + "', null, 0);"
	if out, serr := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); serr != nil {
		t.Fatalf("sqlite3: %v %s", serr, out)
	}
	dir, cmd, err = resumeCommand(s)
	if err != nil || filepath.Clean(dir) != filepath.Clean(ws) || cmd != "mcode --session mvs_0f1e2d3c" {
		t.Fatalf("with the workspace: resume = %q %q %v", dir, cmd, err)
	}
	if rerr := os.RemoveAll(ws); rerr != nil {
		t.Fatal(rerr)
	}
	dir, cmd, err = resumeCommand(s)
	if err != nil || dir != "" || cmd != "mcode --session mvs_0f1e2d3c" {
		t.Fatalf("workspace gone: resume = %q %q %v", dir, cmd, err)
	}
}
