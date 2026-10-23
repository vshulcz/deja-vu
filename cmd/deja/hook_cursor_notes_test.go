package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Cursor shows a hook's user_message only when the hook blocks and has no
// systemMessage field at all, so a note handed to it is marked shown and read
// by nobody. The once-only ones are then gone for Claude Code on the same index.
func TestCursorSessionStartLeavesTheNotesForAHostThatShowsThem(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	t.Cleanup(func() { cursorHookHost = false })
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	q := `{"type":"user","message":{"role":"user","content":"why does the retry loop drop the last attempt"},"timestamp":"2026-03-01T10:00:00Z","sessionId":"%s","cwd":"/proj"}`
	for _, id := range []string{"s1", "s2"} {
		line := strings.Replace(q, "%s", id, 1)
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	type reply struct {
		SystemMessage      string `json:"systemMessage"`
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	run := func(payload string) reply {
		t.Helper()
		withHookStdin(t, payload)
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		var r reply
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("hook output is not JSON: %q", out)
		}
		return r
	}

	// What cursor-agent 2026.09.02 sends on sessionStart.
	cursor := run(`{"conversation_id":"c1","generation_id":"c1","hook_event_name":"sessionStart","cursor_version":"2026.09.02-c22c1a3","workspace_roots":["/proj"],"is_background_agent":false,"composer_mode":"agent"}`)
	if cursor.SystemMessage != "" {
		t.Errorf("Cursor was handed a note it never shows: %q", cursor.SystemMessage)
	}
	if !strings.Contains(cursor.HookSpecificOutput.AdditionalContext, "retry loop") {
		t.Errorf("Cursor's model lost the digest: %q", cursor.HookSpecificOutput.AdditionalContext)
	}

	claude := run(`{"session_id":"cc1","hook_event_name":"SessionStart","source":"startup","cwd":"/proj"}`)
	if !strings.Contains(claude.SystemMessage, "deja indexed 2 sessions") {
		t.Fatalf("the built note was spent on Cursor and Claude Code never heard it: %q", claude.SystemMessage)
	}
}

func TestCursorPromptGetsTheRecallWithoutTheReceipt(t *testing.T) {
	ask := func(t *testing.T, payload func(cwd string) string) sessionStartHookResponse {
		t.Helper()
		hermeticEnv(t)
		t.Cleanup(func() { cursorHookHost = false })
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-hay", "focus.jsonl"), "focus", []string{
			`{"type":"user","sessionId":"focus","timestamp":"` + old + `","message":{"role":"user","content":"the quetzalcoatl stampede fix: jittered ttl"}}`,
		})
		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(t.TempDir(), "tmp", "hay")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		var out bytes.Buffer
		if err := runHookPrompt(index.DefaultDir(), strings.NewReader(payload(cwd)), &out); err != nil {
			t.Fatal(err)
		}
		var r sessionStartHookResponse
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("hook output is not JSON: %q", out.String())
		}
		return r
	}
	const prompt = "quetzalcoatl stampede regression again"

	// The control: the same ask from Claude Code carries the receipt, so the
	// assertion below is not passing on a recall that never spoke.
	claude := ask(t, func(string) string {
		return `{"prompt":"` + prompt + `","session_id":"s-new","hook_event_name":"UserPromptSubmit"}`
	})
	if !strings.Contains(claude.SystemMessage, "you have been here") {
		t.Fatalf("the control did not get its receipt: %+v", claude)
	}

	var spent bool
	cursor := ask(t, func(cwd string) string {
		root := mustMarshal(t, cwd)
		return `{"conversation_id":"c1","generation_id":"g1","hook_event_name":"beforeSubmitPrompt","cursor_version":"2026.09.02-c22c1a3","workspace_roots":[` + string(root) + `],"prompt":"` + prompt + `","attachments":[]}`
	})
	if _, err := os.Stat(index.DefaultDir() + ".dejavu"); err == nil {
		spent = true
	}
	if cursor.HookSpecificOutput.AdditionalContext != claude.HookSpecificOutput.AdditionalContext {
		t.Errorf("Cursor's model got a different recall:\n%s\n---\n%s", cursor.HookSpecificOutput.AdditionalContext, claude.HookSpecificOutput.AdditionalContext)
	}
	if cursor.SystemMessage != "" {
		t.Errorf("Cursor was handed a receipt it never shows: %q", cursor.SystemMessage)
	}
	// The receipt's window stays unspent for the host that does show it.
	if spent {
		t.Error("the receipt's window was spent on a host that shows nobody the receipt")
	}
}

// --strict and --copilot blank the line after it is built, so the notes have
// to be left unbuilt there too or they are marked shown and lost.
func TestStrictAndCopilotSessionStartsLeaveTheNotesUnspent(t *testing.T) {
	for _, tc := range []struct{ flag, payload string }{
		{"--strict", `{"session_id":"z1","hook_event_name":"SessionStart","source":"startup","cwd":"/proj"}`},
		{"--copilot", `{"sessionId":"cp1","source":"new","cwd":"/proj"}`},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("DEJA_WARMUP_SENTINEL", "")
			oldSpawn := spawnWarmup
			spawnWarmup = func(_, _ string) error { return nil }
			t.Cleanup(func() { spawnWarmup = oldSpawn })
			t.Cleanup(func() { strictHookOutput, copilotHookOutput = false, false })
			store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
			if err := os.MkdirAll(store, 0o755); err != nil {
				t.Fatal(err)
			}
			line := `{"type":"user","message":{"role":"user","content":"why does the retry loop drop the last attempt"},"timestamp":"2026-03-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
			if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRunStderr(t, "index"); err != nil {
				t.Fatal(err)
			}
			withHookStdin(t, tc.payload)
			out, err := captureRun(t, "hook-context", tc.flag)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "retry loop") {
				t.Fatalf("the model lost the digest under %s: %q", tc.flag, out)
			}
			dir := index.DefaultDir()
			for _, mark := range []string{".builtnote", ".receipt"} {
				if _, err := os.Stat(dir + mark); err == nil {
					t.Errorf("%s was written under %s, a reply that shows nobody the note", mark, tc.flag)
				}
			}
		})
	}
}
