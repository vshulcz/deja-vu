package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeClaudeMemory(t *testing.T, project, name, typ, desc string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-"+project, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name+".md")
	body := "---\nname: " + name + "\ndescription: \"" + desc + "\"\nmetadata:\n  type: " + typ + "\n---\n\nbody of " + name + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// What Claude Code wrote in its auto-memory reaches every other agent that
// starts in the project. Before, a rule the user taught Claude here was news
// to the Codex session opened in the same directory an hour later.
func TestOtherAgentsStartWithWhatClaudeLearned(t *testing.T) {
	dir, fork := forkStore(t, false)
	rule := writeClaudeMemory(t, "q", "no-sleep", "feedback", "retries use the shared backoff (zebra-quartz-17)")
	writeClaudeMemory(t, "q", "staging", "project", "staging is pg-two (lilac-anvil-9)")
	writeClaudeMemory(t, "p", "no-sleep", "feedback", "retries use the shared backoff (zebra-quartz-17)")
	// The index file is Claude's own list of the others, not a memory.
	_ = os.WriteFile(filepath.Join(filepath.Dir(rule), "MEMORY.md"), []byte("- [x](no-sleep.md) — index-only-token\n"), 0o644)
	codex := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "10", "02", "rollout-2026-10-02T07-00-00-source-x.jsonl")
	run := func(sid, transcript, cwd string) string {
		payload, err := json.Marshal(map[string]string{"session_id": sid, "source": "startup", "transcript_path": transcript, "cwd": cwd})
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	out := run("source-x", codex, "/w/q")
	if !strings.Contains(out, "deja-recall") {
		t.Fatalf("the codex session got no digest at all, so this proves nothing:\n%s", out)
	}
	if !strings.Contains(out, "zebra-quartz-17") || !strings.Contains(out, "lilac-anvil-9") {
		t.Errorf("a codex session did not get what Claude learned here:\n%s", out)
	}
	if strings.Index(out, "zebra-quartz-17") > strings.Index(out, "lilac-anvil-9") {
		t.Errorf("a fact came before a rule:\n%s", out)
	}
	if strings.Contains(out, "index-only-token") {
		t.Errorf("MEMORY.md was read as a memory:\n%s", out)
	}

	// Claude loads its memory itself; handing it over again is bytes twice.
	claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	if out := run(fork, claude, "/w/p"); !strings.Contains(out, "deja-recall") {
		t.Fatalf("the claude session got no digest:\n%s", out)
	} else if strings.Contains(out, "zebra-quartz-17") {
		t.Errorf("Claude Code was handed its own memory:\n%s", out)
	}

	// A line the person set aside on the Memory tab is not handed on.
	if err := setMemHidden(dir, rule, true); err != nil {
		t.Fatal(err)
	}
	if out := run("source-y", codex, "/w/q"); strings.Contains(out, "zebra-quartz-17") || !strings.Contains(out, "lilac-anvil-9") {
		t.Errorf("after hiding the rule:\n%s", out)
	}
}

// The trust policy that withholds a project's sessions from session start
// withholds its memory too: it is the same project's content.
func TestClaudeMemoryFollowsTheTrustPolicy(t *testing.T) {
	dir, _ := forkStore(t, false)
	writeClaudeMemory(t, "q", "no-sleep", "feedback", "retries use the shared backoff (zebra-quartz-17)")
	if got := claudeMemoryBlock(dir, "/w/q", 8, 1000); !strings.Contains(got, "zebra-quartz-17") {
		t.Fatalf("no block without a policy, so this proves nothing: %q", got)
	}
	writePolicy(t, `{"activations":{"auto":{"local":false}}}`)
	if got := claudeMemoryBlock(dir, "/w/q", 8, 1000); got != "" {
		t.Errorf("a project withheld from auto handed on its memory: %q", got)
	}
	writePolicy(t, `{"ignore":["/w/q"]}`)
	if got := claudeMemoryBlock(dir, "/w/q", 8, 1000); got != "" {
		t.Errorf("an ignored directory handed on its memory: %q", got)
	}
}
