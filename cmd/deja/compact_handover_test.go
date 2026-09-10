package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The block deja serves after a compaction named the files and commands a
// summary drops (#543) and not what the session was doing. Measured on real
// transcripts, a session after a compaction spends a median of 28 actions before
// its next edit against 10 from a cold start, over 86 compactions — working out
// the two things that were in the transcript all along: the task, in 94% of
// them, and a decision it had reached, in 94%.
func TestTheCompactBlockSaysWhatTheSessionWasDoing(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "app", "compacting.jsonl"), "compacting", []string{
		`{"type":"user","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"user","content":"cap the quokkabloom retries and warm the cold path first"}}`,
		`{"type":"assistant","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit",` +
			`"input":{"file_path":"/work/app/internal/fetch/quokkabloom.go","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"user","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"e1","content":"ok"}]}}`,
		`{"type":"assistant","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash",` +
			`"input":{"command":"go test ./internal/fetch -count=1"}}]}}`,
		`{"type":"user","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok  internal/fetch  1.2s"}]}}`,
		`{"type":"assistant","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"Decision: the quokkabloom retry budget stays at four."}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	got := compactEvidence(index.DefaultDir(), "compacting", "/work/app")
	if got == "" {
		t.Fatal("the compaction block is empty, so this measures nothing")
	}
	if !strings.Contains(got, "was working on: cap the quokkabloom retries") {
		t.Errorf("the block does not say what the session was doing:\n%s", got)
	}
	if !strings.Contains(got, "had settled: Decision: the quokkabloom retry budget stays at four") {
		t.Errorf("the block does not say what the session settled:\n%s", got)
	}
	// And it still carries the specifics a summary drops.
	if !strings.Contains(got, "quokkabloom.go") || !strings.Contains(got, "go test ./internal/fetch") {
		t.Errorf("the block lost the files or the commands:\n%s", got)
	}
}
