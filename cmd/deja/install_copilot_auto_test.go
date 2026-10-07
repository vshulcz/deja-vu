package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// deja's hook file under ~/.copilot/hooks, which Copilot CLI and VS Code
// Copilot Chat both read.
type copilotHookFile struct {
	Hooks map[string][]struct {
		Type       string `json:"type"`
		Bash       string `json:"bash"`
		TimeoutSec int    `json:"timeoutSec"`
		Matcher    string `json:"matcher"`
	} `json:"hooks"`
}

func copilotTestHome(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	t.Setenv("COPILOT_HOME", "")
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func readCopilotHookFile(t *testing.T) (copilotHookFile, string) {
	t.Helper()
	got := readFile(t, copilotHooksPath())
	var cfg copilotHookFile
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatalf("the hook file is not JSON: %v\n%s", err, got)
	}
	return cfg, got
}

// Every surface deja has, on the events a stand against Copilot CLI 1.0.92
// showed reach the model, and PreCompact spelled the way VS Code reads it.
func TestInstallCopilotAutoWiresEverySurfaceInItsOwnFile(t *testing.T) {
	home := copilotTestHome(t)
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".copilot", "hooks", "deja.json"); copilotHooksPath() != want {
		t.Fatalf("hook file at %s, want %s", copilotHooksPath(), want)
	}
	cfg, first := readCopilotHookFile(t)
	for event, want := range map[string]struct{ cmd, matcher string }{
		"sessionStart":        {" hook-context --copilot", ""},
		"userPromptSubmitted": {" hook-prompt --copilot", ""},
		"preToolUse":          {" hook-tool --copilot", "bash|powershell|edit|create"},
		"postToolUse":         {" hook-tool-after --copilot", "bash|powershell"},
		"postToolUseFailure":  {" hook-tool-after --copilot", "bash|powershell"},
		"PreCompact":          {" hook-precompact", ""},
		"preMcpToolCall":      {" hook-mcp-call", ""},
		"sessionEnd":          {" hook-session-end", ""},
	} {
		entries := cfg.Hooks[event]
		if len(entries) != 1 {
			t.Errorf("%s: %d entries, want 1:\n%s", event, len(entries), first)
			continue
		}
		e := entries[0]
		if e.Type != "command" || !strings.HasSuffix(e.Bash, want.cmd) || e.Matcher != want.matcher {
			t.Errorf("%s = %+v, want %q matcher %q", event, e, want.cmd, want.matcher)
		}
		// Copilot waits on a hook before the request it belongs to, so a slow
		// deja must not hold the prompt for the default minute.
		if e.TimeoutSec <= 0 || e.TimeoutSec > 10 {
			t.Errorf("%s timeoutSec = %d, want a few seconds", event, e.TimeoutSec)
		}
	}
	if len(cfg.Hooks) != 8 {
		t.Errorf("hook file carries %d events, want 8:\n%s", len(cfg.Hooks), first)
	}
	if _, err := os.Stat(filepath.Join(home, ".copilot", "config.json")); err == nil {
		t.Errorf("install wrote Copilot's own config.json")
	}
	// settings.json gets the status line and nothing else.
	var settings map[string]any
	if b, err := os.ReadFile(filepath.Join(home, ".copilot", "settings.json")); err != nil || json.Unmarshal(b, &settings) != nil {
		t.Fatalf("no status line in settings.json: %v", err)
	}
	line, _ := settings["statusLine"].(map[string]any)
	if cmd, _ := line["command"].(string); len(settings) != 1 || !strings.HasSuffix(cmd, " statusline") {
		t.Errorf("settings.json = %v, want deja's statusLine alone", settings)
	}

	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, copilotHooksPath()); again != first {
		t.Errorf("a second install changed the file:\nfirst:\n%s\nsecond:\n%s", first, again)
	}
	if _, err := captureRun(t, "uninstall", "copilot-auto"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(copilotHooksPath()); err == nil {
		t.Errorf("uninstall left the hook file deja created:\n%s", b)
	}
}

// An install from before the hook file left deja's entries in settings.json,
// or in a config.json Copilot had not moved yet. The new install takes them
// out, so an upgrade does not run deja twice, and leaves the reader's own.
func TestInstallCopilotAutoClearsTheEntriesOlderInstallsWrote(t *testing.T) {
	home := copilotTestHome(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	config := filepath.Join(home, ".copilot", "config.json")
	deja := hookExeInConfigs("/usr/local/bin/deja")
	legacy := func(extra string) string {
		return `{"model":"gpt-5.4","hooks":{"sessionStart":[{"type":"command","bash":"/usr/bin/theirs start","timeoutSec":5},{"type":"command","bash":"` + deja + ` hook-context --copilot","timeoutSec":10}],"postToolUse":[{"type":"command","bash":"` + deja + ` hook-tool-after --copilot","timeoutSec":10}]` + extra + `}}` + "\n"
	}
	if err := os.WriteFile(settings, []byte(legacy("")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(copilotManagedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, settings)
	if strings.Contains(got, "hook-context") || strings.Contains(got, "hook-tool-after") {
		t.Errorf("deja's old entries are still in settings.json:\n%s", got)
	}
	if !strings.Contains(got, "/usr/bin/theirs start") || !strings.Contains(got, `"model"`) {
		t.Errorf("install took what was not deja's:\n%s", got)
	}
	if got := readFile(t, config); got != copilotManagedConfig {
		t.Errorf("install rewrote Copilot's own config.json:\n%s", got)
	}
	if cfg, _ := readCopilotHookFile(t); len(cfg.Hooks["sessionStart"]) != 1 {
		t.Errorf("the hook file does not carry the digest")
	}
}

// A comment deja cannot keep is a refusal, and a refusal writes nothing: the
// MCP entry and the hook file used to be written first, leaving half a target.
func TestCopilotAutoRefusesBeforeWritingAnything(t *testing.T) {
	home := copilotTestHome(t)
	config := filepath.Join(home, ".copilot", "config.json")
	before := "{\n  // mine\n  \"hooks\": {\"sessionStart\": [{\"type\": \"command\", \"bash\": \"" + hookExeInConfigs("/usr/local/bin/deja") + " hook-context --copilot\"}]}\n}\n"
	if err := os.WriteFile(config, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err == nil {
		t.Fatal("install edited hooks in a file whose comments it cannot keep")
	}
	for _, name := range []string{"mcp-config.json", "settings.json", filepath.Join("hooks", "deja.json"), filepath.Join("skills", "deja-history", "SKILL.md")} {
		if _, err := os.Stat(filepath.Join(home, ".copilot", name)); err == nil {
			t.Errorf("a refused install still wrote %s", name)
		}
	}
	if got := readFile(t, config); got != before {
		t.Errorf("a refused install changed config.json:\n%s", got)
	}
}

// The file is shared: vscode-auto writes the same one, and taking either
// target out leaves it while the other is installed.
func TestCopilotAndVSCodeAutoShareTheHookFile(t *testing.T) {
	copilotTestHome(t)
	if _, err := captureRun(t, "install", "vscode-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	_, first := readCopilotHookFile(t)
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, copilotHooksPath()); again != first {
		t.Errorf("copilot-auto rewrote the file vscode-auto wrote:\nfirst:\n%s\nsecond:\n%s", first, again)
	}
	if _, err := captureRun(t, "uninstall", "copilot-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copilotHooksPath()); err != nil {
		t.Fatalf("uninstalling copilot-auto took the file vscode-auto still needs: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "vscode-auto"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(copilotHooksPath()); err == nil {
		t.Errorf("the last target out left the hook file:\n%s", b)
	}
}

// Copilot reads `additionalContext` at the top level and nothing nested: the
// Claude envelope runs, succeeds, and reaches no one. The payload names the
// session as `sessionId`, and that session stays out of its own digest.
func TestHookContextCopilotAnswersInCopilotsShape(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	seedClaudeAt(t, claude, "app", "older-session", "the ledger export drops the last row", "the writer missed a final flush", now.Add(-2*time.Hour))
	seedClaudeAt(t, claude, "app", "live-session", "the glimmerquest cache misses on every cold start", "warming it at boot from the last snapshot fixed it", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	was := copilotHookOutput
	copilotHookOutput = true
	defer func() { copilotHookOutput = was }()

	withHookStdin(t, `{"sessionId":"live-session","timestamp":1790866280348,"cwd":"/tmp/app","source":"new","initialPrompt":"fix the export"}`)
	out := captureStdout(t, func() {
		if err := runHookContext(dir, false); err != nil {
			t.Error(err)
		}
	})
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &resp); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out)
	}
	if len(resp) != 1 {
		t.Errorf("want only additionalContext, got keys %v", resp)
	}
	ctx, _ := resp["additionalContext"].(string)
	if !strings.Contains(ctx, "ledger export") {
		t.Fatalf("the digest for the payload's project is missing:\n%s", out)
	}
	if strings.Contains(ctx, "glimmerquest") {
		t.Errorf("the session asking was served its own prompt:\n%s", out)
	}
}

func TestDoctorReportsCopilotAutoRecall(t *testing.T) {
	home := copilotTestHome(t)
	row := func() string {
		var out bytes.Buffer
		doctorAutoRecall(&out)
		// The row and the lines printed under it, which are indented past the
		// name column.
		var lines []string
		on := false
		for _, line := range strings.Split(out.String(), "\n") {
			switch {
			case strings.HasPrefix(line, "  copilot "):
				on = true
			case !strings.HasPrefix(line, strings.Repeat(" ", 15)):
				on = false
			}
			if on {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	if got := row(); !strings.Contains(got, "missing") {
		t.Fatalf("no row, or not missing before install:\n%s", got)
	}
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if got := row(); !strings.Contains(got, "wired") {
		t.Errorf("not wired after install:\n%s", got)
	}

	// The switch that turns every hook off leaves the entry looking installed.
	settings := filepath.Join(home, ".copilot", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := row(); !strings.Contains(got, "switched off") || !strings.Contains(got, "disableAllHooks") {
		t.Errorf("disableAllHooks is on and the row does not say so:\n%s", got)
	}
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}

	// An entry naming a binary that is gone is a hook that exits 127. Absolute
	// the way this platform spells it: "/gone/deja" is relative on Windows.
	gone := filepath.Join(t.TempDir(), "gone", "deja")
	dead := `{"hooks":{"userPromptSubmitted":[{"type":"command","bash":"` + jsonEscaped(t, gone) + ` hook-prompt --copilot","timeoutSec":10}]}}`
	if err := os.WriteFile(copilotHooksPath(), []byte(dead), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := row(); !strings.Contains(got, gone) || !strings.Contains(got, "copilot-auto") {
		t.Errorf("a dead binary in the hook is not reported:\n%s", got)
	}
}

// What Copilot CLI 1.0.79 leaves in config.json once it has moved the user's
// settings into settings.json: two comment lines of its own, then JSON.
const copilotManagedConfig = "// User settings belong in settings.json.\n// This file is managed automatically.\n{\n  \"firstLaunchAt\": \"2026-03-11T00:00:00.000Z\"\n}\n"

// Uninstalling the base target takes the hook with it, the way claude-code
// takes claude-auto's: a hook calling a server that is gone is half a target.
func TestUninstallCopilotTakesTheAutoHookToo(t *testing.T) {
	copilotTestHome(t)
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "copilot"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(copilotHooksPath()); err == nil && strings.Contains(string(b), "hook-context") {
		t.Errorf("`deja uninstall copilot` left the hook:\n%s", b)
	}
}

// A hook-prompt line without --copilot answers in Claude's envelope, which
// Copilot CLI runs and drops: installed-looking, delivering nothing.
func TestDoctorCallsAPlainHookPromptEntryStale(t *testing.T) {
	copilotTestHome(t)
	if err := os.MkdirAll(filepath.Dir(copilotHooksPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copilotHooksPath(), []byte(`{"hooks":{"userPromptSubmitted":[{"type":"command","bash":"deja hook-prompt","timeoutSec":10}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, a := range autoWirings() {
		if a.name != "copilot" {
			continue
		}
		if state, _ := autoWiringState(a); state != "stale" {
			t.Errorf("state = %q for a hook Copilot drops the answer of, want stale", state)
		}
		return
	}
	t.Fatal("no copilot row")
}

// COPILOT_HOME moves every file Copilot CLI keeps, and a relative one is used
// as written, as Copilot's own resolveCopilotHome uses it (#4567).
func TestCopilotHomeMovesEveryCopilotPath(t *testing.T) {
	copilotTestHome(t)
	t.Setenv("DEJA_COPILOT_ROOT", "")
	abs := filepath.Join(t.TempDir(), "ch")
	t.Setenv("COPILOT_HOME", abs)
	for name, got := range map[string]string{
		"session root": sources.CopilotRoot(),
		"skill":        guidancePath("copilot"),
		"hooks":        copilotHooksPath(),
		"mcp":          copilotMCPConfigPath(),
	} {
		if !strings.HasPrefix(got, abs+string(filepath.Separator)) {
			t.Errorf("%s = %s, want under COPILOT_HOME %s", name, got, abs)
		}
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("COPILOT_HOME", "relch")
	want := filepath.Join("relch", "session-state")
	if got := sources.CopilotRoot(); got != want {
		t.Errorf("relative COPILOT_HOME: session root = %s, want %s", got, want)
	}
	// The deja override still wins for the session root.
	t.Setenv("DEJA_COPILOT_ROOT", filepath.Join(cwd, "mine"))
	if got := sources.CopilotRoot(); got != filepath.Join(cwd, "mine") {
		t.Errorf("DEJA_COPILOT_ROOT lost to COPILOT_HOME: %s", got)
	}
}

// PowerShell runs a quoted path only behind `&`, and inside single quotes the
// only character that means anything is the quote itself, doubled.
func TestCopilotPowerShellLineQuotesThePath(t *testing.T) {
	got := copilotPowerShellCommand(`C:\Users\O'Brien\$x (1)\deja.exe`, "hook-context", "--copilot")
	want := `& 'C:/Users/O''Brien/$x (1)/deja.exe' hook-context --copilot`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Copilot writes settings.json itself, so the file being there says nothing
// about deja: a machine that never ran copilot-auto reads missing.
func TestDoctorCallsCopilotsOwnSettingsMissing(t *testing.T) {
	home := copilotTestHome(t)
	if err := os.WriteFile(filepath.Join(home, ".copilot", "settings.json"), []byte(`{"model":"gpt-5.4"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, a := range autoWirings() {
		if a.name != "copilot" {
			continue
		}
		if state, _ := autoWiringState(a); state != "missing" {
			t.Errorf("state = %q for Copilot's own settings with no deja hook, want missing", state)
		}
		return
	}
	t.Fatal("no copilot row")
}
