package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Every other surface puts a promoted note in front of the transcript it was
// distilled from. The per-prompt hook ranks sessions and never builds a
// search.Hit, so the rule could not reach it and the block led with the
// transcript while its own note waited behind (#2803).
func TestTheHookLiftsANoteAboveItsSource(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	// The transcript says the question's words several times over; the note is
	// the one line distilled from it. That is the shape the rule exists for —
	// on score alone the source wins its own distillation.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "s14", []string{
		`{"type":"user","sessionId":"s14","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer pool cap settled at 40 for the ticker service"}}`,
		`{"type":"user","sessionId":"s14","timestamp":"` + old +
			`","message":{"role":"user","content":"what should the pgbouncer pool cap be for the ticker service"}}`,
		`{"type":"user","sessionId":"s14","timestamp":"` + old +
			`","message":{"role":"user","content":"the pgbouncer pool cap keeps coming up, cap the pool"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s14"); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we settle for the pgbouncer pool cap","session_id":"ses_ask"}`)
	if err := runHookPromptMode(dir, in, &out, true); err != nil {
		t.Fatal(err)
	}
	block := out.String()
	// The ids as the block prints them, in backticks. The note's own line
	// carries "-s14`" and not "`s14`", so the two needles do not collide — an
	// earlier version of this test searched for "· s14", which is not how the
	// line is written, and the order below was never actually checked.
	note := strings.Index(block, "`deja-note-claude-s14`")
	source := strings.Index(block, "`s14`")
	// Not a skip: a block holding neither is a fixture that stopped reaching
	// the path, and a test that goes quiet there guards nothing (#1412).
	if note < 0 {
		t.Fatalf("the note promoted from the session is not in the block:\n%s", block)
	}
	if source < 0 {
		t.Fatalf("the transcript is not in the block, so there is no order to check:\n%s", block)
	}
	if note > source {
		t.Errorf("the transcript came before the note distilled from it:\n%s", block)
	}
}
