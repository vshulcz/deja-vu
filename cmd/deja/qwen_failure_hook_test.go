package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Qwen fires PostToolUse only when the tool succeeded — its own code guards the
// call with `!toolResult.error` and calls the failure hook otherwise — so the
// fix pair sat on the one event that never fires at a failure. Measured on
// qwen-code 0.20.0: a failing run_shell_command fires PostToolUseFailure, and
// what that hook returns is appended to the tool result the model reads.
func TestQwenWiresTheFixPairToTheFailureEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".qwen", "settings.json"))
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
		t.Fatalf("settings are not JSON qwen can read: %v\n%s", err, b)
	}
	find := func(event string) (string, string, bool) {
		for _, e := range cfg.Hooks[event] {
			for _, h := range e.Hooks {
				if strings.Contains(h.Command, "/bin/deja") {
					return h.Command, e.Matcher, true
				}
			}
		}
		return "", "", false
	}
	cmd, matcher, ok := find("PostToolUseFailure")
	if !ok {
		t.Fatalf("no deja hook on PostToolUseFailure:\n%s", b)
	}
	if !strings.Contains(cmd, "hook-tool-after") {
		t.Fatalf("the failure event runs %q, not the fix pair", cmd)
	}
	if matcher != "run_shell_command" {
		t.Fatalf("the fix pair fires for every tool (matcher %q), not just the one that runs commands", matcher)
	}
	if _, _, ok := find("PostToolUse"); ok {
		t.Fatalf("the old PostToolUse hook is still there; it fires only when the command worked:\n%s", b)
	}
	if cmd, _, ok := find("PreCompact"); !ok || !strings.Contains(cmd, "hook-precompact") {
		t.Fatalf("nothing forgets after a compaction:\n%s", b)
	}
	// PreToolUse fires on qwen too, and what it returns never reaches the
	// model — a process per command for nothing.
	if len(cfg.Hooks["PreToolUse"]) != 0 {
		t.Errorf("PreToolUse is wired, and its output is not context:\n%s", b)
	}
	// Reinstalling must not double any of them.
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(filepath.Join(home, ".qwen", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(b2) {
		t.Errorf("a second install changed the file:\n%s\n%s", b, b2)
	}
}

// An install that ran before the fix left the PostToolUse entry behind; a
// re-install has to take it, or a repair lookup keeps running after every
// command that worked.
func TestQwenReinstallRetiresTheOldPostToolUseHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".qwen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// What the older deja wrote, beside a hook of the user's own.
	before := `{"hooks":{"PostToolUse":[` +
		`{"matcher":"run_shell_command","hooks":[{"type":"command","command":"/bin/deja hook-tool-after","timeout":60000}]},` +
		`{"matcher":"write_file","hooks":[{"type":"command","command":"my-own-hook","timeout":1000}]}]}}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if strings.Contains(got, `"command":"/bin/deja hook-tool-after","timeout":60000}]},{"matcher":"write_file"`) {
		t.Fatalf("deja's old PostToolUse entry survived:\n%s", got)
	}
	if strings.Count(got, "hook-tool-after") != 1 {
		t.Fatalf("the fix pair is wired %d times:\n%s", strings.Count(got, "hook-tool-after"), got)
	}
	if !strings.Contains(got, "my-own-hook") {
		t.Fatalf("the user's own PostToolUse hook was taken with it:\n%s", got)
	}
}

// Qwen sends the command's output under `error`, inside a block it wrote
// itself. Both halves matter: reading only tool_response finds nothing, and
// scanning the frame hashes `Output: <error>` — a signature no session ever
// recorded — so the pair went quiet for every qwen failure.
func TestFixPairReadsQwensFramedFailure(t *testing.T) {
	seedFixPair(t, "./main.go:9:2: undefined: glimwraxHelper", "glimwraxctl --sync && go build ./...")
	payload := `{"session_id":"qwen-1","cwd":"/work/app","hook_event_name":"PostToolUseFailure",` +
		`"tool_name":"run_shell_command","tool_input":{"command":"go build ./..."},` +
		`"error":"Command: go build ./...\nDirectory: (root)\nOutput: ./main.go:9:2: undefined: glimwraxHelper\nError: (none)\nExit Code: 1\n"}`
	var out bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "glimwraxctl --sync") {
		t.Fatalf("the repair did not survive qwen's framing:\n%s", out.String())
	}
}

// Output that never carried the frame passes through untouched. Plenty of real
// output has a line starting with "Error:" without being a report at all — node
// prints one for every uncaught throw — so the unwrap is reached only for text
// that opens with "Command:", the way qwen's report does.
func TestFixPairLeavesUnframedOutputAlone(t *testing.T) {
	seedFixPair(t, "Error: Cannot find module 'glimwrax'", "npm ci")
	node := `npm ERR! code ELIFECYCLE\nError: Cannot find module 'glimwrax'\n    at Function.Module._resolveFilename`
	payload := `{"session_id":"qwen-2","cwd":"/work/app","hook_event_name":"PostToolUseFailure",` +
		`"tool_name":"run_shell_command","tool_input":{"command":"npm start"},"error":"` + node + `"}`
	var out bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "npm ci") {
		t.Fatalf("a stack trace was mistaken for a frame and lost its error line:\n%s", out.String())
	}
}
