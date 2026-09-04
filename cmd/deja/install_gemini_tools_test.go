package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Gemini appends what an AfterTool hook returns to the tool result as
// <hook_context>, and deja was not listening: a gemini session got the project
// digest and the prompt answer, and nothing at the moment a command failed.
// Checked on gemini-cli 0.55.1 — the hook fires, its context reaches the model,
// and a matcher scopes it to the tool that runs commands.
func TestInstallGeminiWiresTheFixPair(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGeminiExtension("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "extensions", "deja", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	for event, sub := range map[string]string{
		"AfterTool": "hook-tool-after",
	} {
		entries := root.Hooks[event]
		if len(entries) != 1 {
			t.Fatalf("%s entries = %d, want 1: %s", event, len(entries), b)
		}
		if got := entries[0].Hooks[0].Command; !strings.HasSuffix(got, sub) {
			t.Errorf("%s runs %q, want %s", event, got, sub)
		}
		// Gemini reads the timeout in milliseconds; a Claude-style 10 kills the
		// hook before it can answer.
		if got := entries[0].Hooks[0].Timeout; got < 1000 {
			t.Errorf("%s timeout = %d, too short to be milliseconds", event, got)
		}
	}
	// Scoped to the tool that runs a command, so the hook does not spawn on
	// every read_file and glob.
	if m := root.Hooks["AfterTool"][0].Matcher; m != "run_shell_command" {
		t.Errorf("AfterTool matcher = %q, want the tool that runs commands", m)
	}
	// BeforeTool fires too, and what it returns never reaches the model — a
	// process per command for nothing.
	if len(root.Hooks["BeforeTool"]) != 0 {
		t.Errorf("BeforeTool is wired, and its output is not context: %s", b)
	}
}

// The names and the output field are gemini's own, and recognising them is what
// makes the wiring above say anything at all.
func TestGeminiToolNamesAndOutputAreRead(t *testing.T) {
	if !isCommandTool("run_shell_command") {
		t.Error("run_shell_command is not read as a command tool, so the fix pair never fires on gemini")
	}
	got := toolResponseText(json.RawMessage(`{"llmContent":"make: *** [test] Error 1"}`))
	if !strings.Contains(got, "Error 1") {
		t.Errorf("gemini's llmContent was not read: %q", got)
	}
	// And the frame gemini wraps it in comes off. The "Output:" marker is the
	// one that matters: with it in front, the first line of a build failure
	// stops reading as an error and the fix pair says nothing.
	wrapped := `{"llmContent":"<untrusted_context>\nOutput: ./main.go:12:2: undefined: zorbquuxHelper\nProcess Group PGID: 98883\n</untrusted_context>"}`
	got = toolResponseText(json.RawMessage(wrapped))
	if !strings.HasPrefix(strings.TrimSpace(got), "./main.go:12:2:") {
		t.Errorf("gemini's wrapper was not stripped: %q", got)
	}
}
