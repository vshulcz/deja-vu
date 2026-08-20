package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// What must not repeat is what the reader sees, not where it came from. Banning
// the session banned everything else it held: measured on a real store, walking
// one working session of eighty messages, the second half got three blocks
// where the same messages without the ban got thirty-eight — and only seven of
// those were word-for-word repeats. The same past work answers many questions
// in a day.
func TestBlockFingerprintIgnoresSpacingAndSeparatesContent(t *testing.T) {
	a := blockFingerprint("- Assistant: pgbouncer pool size settled at 40\n")
	b := blockFingerprint("- Assistant:   pgbouncer pool size settled at 40")
	if a != b {
		t.Errorf("the same block fingerprinted differently after re-wrapping: %s vs %s", a, b)
	}
	c := blockFingerprint("- Assistant: pgbouncer pool size settled at 41")
	if a == c {
		t.Error("two different blocks share a fingerprint")
	}
}

// The cooldown counts injections, not minutes: a run of messages about one
// thing should not keep re-reading the same session, and a session that comes
// back to it later should get it again.
func TestRecentlyInjectedCountsTheLastFew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	var b strings.Builder
	b.WriteString("agent old-1 2020-01-01T00:00:00Z\n")
	for i := 0; i < injectionCooldown; i++ {
		b.WriteString("agent filler 2020-01-01T00:00:00Z\n")
	}
	b.WriteString("agent fresh-1 2020-01-01T00:00:00Z\n")
	if err := os.WriteFile(dir+".hookseen", []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	got := recentlyInjected(dir, "agent", injectionCooldown)
	if !got["fresh-1"] {
		t.Error("a session shown a moment ago is not in the window")
	}
	if got["old-1"] {
		t.Error("a session shown long before the window still counts as recent")
	}
	if other := recentlyInjected(dir, "someone-else", injectionCooldown); len(other) != 0 {
		t.Errorf("another agent session's history leaked in: %v", other)
	}
}

// Entries written before this carried two fields, and the plan hook still
// writes two. Both shapes have to read back.
func TestAlreadyInjectedReadsBothLineShapes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.WriteFile(dir+".hookseen",
		[]byte("agent two-fields\nagent three-fields 2020-01-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := alreadyInjected(dir, "agent")
	for _, want := range []string{"two-fields", "three-fields"} {
		if !got[want] {
			t.Errorf("%q was not read back from the seen list: %v", want, got)
		}
	}
}

// Asking the same thing twice in one agent session must not print the same
// block twice: the second time it is wallpaper, and a reader who sees wallpaper
// stops reading. What is banned is the text, so the session it came from can
// still answer the next question with other lines.
func TestHookPromptDoesNotRepeatTheSameBlock(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "repeatterm", []string{
		`{"type":"user","sessionId":"repeatterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	payload := `{"prompt":"do we need pgbouncer here","session_id":"agent-1"}`
	var first bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &first, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "transaction mode") {
		t.Fatalf("nothing was recalled the first time:\n%q", first.String())
	}
	// Push that session out of the cooldown window, so the ranking offers it
	// again and the only thing left to stop a repeat is the block itself.
	f, err := os.OpenFile(index.DefaultDir()+".hookseen", os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < injectionCooldown+2; i++ {
		if _, err := f.WriteString("agent-1 filler-" + strconv.Itoa(i) + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var second bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &second, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second.String(), "transaction mode") {
		t.Errorf("the same block was shown twice:\n%q", second.String())
	}
}
