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

// Claude Code 2.1.287 fires PostToolUseFailure for a Bash call that exits
// non-zero, with the output under `error`, and rejects a reply whose
// hookEventName is not the event it sent: "Hook returned incorrect event name:
// expected 'PostToolUseFailure' but got 'PostToolUse'" (#4488).
func TestToolAfterAnswersTheFailureEventItWasSent(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	// The shape captured from a live claude -p run, error text swapped in.
	payload, _ := json.Marshal(map[string]any{
		"session_id":      "agent-fail",
		"cwd":             "/work/app",
		"hook_event_name": "PostToolUseFailure",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "make", "description": "build"},
		"tool_use_id":     "toolu_1",
		"error":           "Exit code 2\ngoroutine 1 [running]:\npanic: sql: database is closed",
		"is_interrupt":    false,
	})
	var out bytes.Buffer
	if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("undecodable hook response %q: %v", out.String(), err)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("the failure payload got no fix pair:\n%s", out.String())
	}
	if got := resp.HookSpecificOutput.HookEventName; got != "PostToolUseFailure" {
		t.Errorf("hookEventName = %q, Claude Code drops a reply that does not name PostToolUseFailure", got)
	}
}

func claudeHookCommands(t *testing.T, event string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(sources.ClaudeConfigDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("settings.json is not JSON: %v\n%s", err, b)
	}
	var got []string
	for _, e := range cfg.Hooks[event] {
		for _, h := range e.Hooks {
			got = append(got, e.Matcher+" "+h.Command)
		}
	}
	return got
}

// A failing Bash never fires PostToolUse in Claude Code, so the fix pair has to
// sit on PostToolUseFailure as well. PostToolUse stays: `go test ./... | tail`
// exits 0 and still prints the error.
func TestClaudeAutoWiresTheFailureEvent(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"model": "opus", "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "/tmp/notify.sh"}]}]}}` + "\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	fail := claudeHookCommands(t, "PostToolUseFailure")
	if len(fail) != 1 || !strings.HasPrefix(fail[0], "Bash ") || !strings.HasSuffix(fail[0], " hook-tool-after") {
		t.Fatalf("PostToolUseFailure = %q, want one Bash entry running hook-tool-after", fail)
	}
	if post := claudeHookCommands(t, "PostToolUse"); len(post) != 1 {
		t.Fatalf("PostToolUse = %q, want it kept", post)
	}
	if _, err := installClaudeHook("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("install then uninstall changed the file:\n%s\n%s", before, after)
	}
}

// Settings an older deja wrote, every event but the failure one, are out of
// date: doctor says so, and the post-upgrade refresh adds the missing entry.
func TestClaudeWiringWithoutTheFailureEventIsRefreshed(t *testing.T) {
	withStatsStores(t)
	old := []string{"SessionStart", "PreCompact", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SessionEnd"}
	writeClaudeSettings(t, old...)
	var out bytes.Buffer
	doctorHooks(&out)
	if !strings.Contains(out.String(), "PostToolUseFailure") {
		t.Errorf("doctor does not name the missing failure event:\n%s", out.String())
	}

	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), []byte(`{"version":"0.14.0","targets":["claude-auto"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshWiringAfterUpgrade()
	if got := claudeHookCommands(t, "PostToolUseFailure"); len(got) != 1 || !strings.HasSuffix(got[0], " hook-tool-after") {
		t.Fatalf("after the upgrade refresh PostToolUseFailure = %q", got)
	}
}

// Claude Code before 2.0.56 has no PostToolUseFailure, and its settings schema
// is a closed list of events: one key it does not know fails the whole of
// ~/.claude/settings.json — "Found invalid settings files … They will be
// ignored" — the user's permissions and model with it. So install leaves the
// event out for a claude that old, takes out one it finds, and doctor does not
// ask for it.
func TestClaudeAutoLeavesTheFailureEventOutForAnOldClaude(t *testing.T) {
	withStatsStores(t)
	t.Cleanup(func() { claudeVersion = claudeVersionReal })
	claudeVersion = func() ([3]int, bool) { return [3]int{2, 0, 55}, true }
	writeClaudeSettings(t, "SessionStart", "PreCompact", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "SessionEnd")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook(exe, false); err != nil {
		t.Fatal(err)
	}
	if got := claudeHookCommands(t, "PostToolUseFailure"); len(got) != 0 {
		t.Errorf("Claude Code 2.0.55 got PostToolUseFailure = %q; it would ignore the whole settings.json", got)
	}
	if got := claudeHookCommands(t, "PostToolUse"); len(got) != 1 {
		t.Errorf("PostToolUse = %q, want it kept", got)
	}
	var out bytes.Buffer
	doctorHooks(&out)
	if strings.Contains(out.String(), "PostToolUseFailure") || !strings.Contains(out.String(), "wired") {
		t.Errorf("doctor asks an old Claude Code for an event it cannot load:\n%s", out.String())
	}

	claudeVersion = func() ([3]int, bool) { return [3]int{2, 0, 56}, true }
	if _, err := installClaudeHook(exe, false); err != nil {
		t.Fatal(err)
	}
	if got := claudeHookCommands(t, "PostToolUseFailure"); len(got) != 1 {
		t.Errorf("Claude Code 2.0.56 got PostToolUseFailure = %q, want it wired", got)
	}
}

// The plugin manifest goes through the same closed list: on Claude Code 2.0.55
// `claude plugin validate` fails it with "hooks: Invalid input" and the plugin
// does not load. hooks/hooks.json is read on its own, and a file that fails
// there is skipped with the manifest's hooks still loaded, so an event newer
// than that lives in hooks.json.
func TestPluginManifestHooksOnlyEventsOldClaudeKnows(t *testing.T) {
	// The event list of Claude Code 2.0.55's settings schema.
	known := map[string]bool{"PreToolUse": true, "PostToolUse": true, "Notification": true,
		"UserPromptSubmit": true, "SessionStart": true, "SessionEnd": true, "Stop": true,
		"SubagentStart": true, "SubagentStop": true, "PreCompact": true, "PermissionRequest": true}
	for event := range hookMatchers(t, repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), "deja.sh") {
		if !known[event] {
			t.Errorf("plugin.json hooks %s, which Claude Code 2.0.55 rejects along with the whole plugin; put it in hooks/hooks.json", event)
		}
	}
	extra := hookMatchers(t, repoFile(t, "claude-plugin/hooks/hooks.json"), "deja.sh")
	if extra["PostToolUseFailure"] != "Bash" {
		t.Errorf("hooks/hooks.json = %v, want PostToolUseFailure on Bash", extra)
	}
}
