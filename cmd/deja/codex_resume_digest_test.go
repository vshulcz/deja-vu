package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Codex keeps the context a hook added in the thread, and resuming it replays
// that into the next request: every `codex resume` put the same start digest in
// front of the model once more, two after one resume and three after a fork
// (#4550). A Claude transcript that holds no digest still gets one on resume
// (#5122 covers the one that does).
func TestCodexResumeGetsNoSecondDigest(t *testing.T) {
	dir, fork := forkStore(t, false)
	codex := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "10", "02")
	source := filepath.Join(codex, "rollout-2026-10-02T07-00-00-source-x.jsonl")
	forked := filepath.Join(codex, "rollout-2026-10-02T07-20-00-fork-x.jsonl")
	prior := filepath.Join(codex, "rollout-2026-08-04T09-00-00-prior-x.jsonl")
	run := func(sid, src, transcript, cwd string) string {
		// Marshalled, not spliced: a Windows transcript path's backslashes
		// spliced in raw are not JSON, and the hook read no payload at all.
		payload, err := json.Marshal(map[string]string{"session_id": sid, "source": src, "transcript_path": transcript, "cwd": cwd})
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
	if out := run("source-x", "startup", source, "/w/q"); !strings.Contains(out, "deja-recall") {
		t.Fatalf("the codex session got no digest at startup, so this proves nothing:\n%s", out)
	}
	if out := run("source-x", "resume", source, "/w/q"); strings.TrimSpace(out) != "" {
		t.Errorf("a resumed codex session got its start digest again:\n%s", out)
	}
	// A fork carries the thread it came from, that digest included.
	if out := run("fork-x", "startup", forked, "/w/q"); strings.TrimSpace(out) != "" {
		t.Errorf("a codex fork got a second start digest:\n%s", out)
	}
	// A thread that never had one gets one on resume.
	if out := run("prior-x", "resume", prior, "/w/q"); !strings.Contains(out, "deja-recall") {
		t.Errorf("a resumed codex session that never had a digest got none:\n%s", out)
	}
	// A Claude transcript holding no digest is given one.
	claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	if out := run(fork, "startup", claude, "/w/p"); !strings.Contains(out, "deja-recall") {
		t.Fatalf("the claude session got no digest at startup:\n%s", out)
	}
	if out := run(fork, "resume", claude, "/w/p"); !strings.Contains(out, "deja-recall") {
		t.Errorf("a resumed Claude Code session lost its digest:\n%s", out)
	}
}
