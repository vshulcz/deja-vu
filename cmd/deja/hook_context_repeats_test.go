package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedClaudeAt is seedClaude with a timestamp of its own, so an ordering test
// is not deciding between sessions that all claim the same instant.
func seedClaudeAt(t *testing.T, root, project, id, userText, asstText string, at time.Time) {
	t.Helper()
	dir := filepath.Join(root, "-"+project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text string) string {
		rec := map[string]any{
			"type": role, "sessionId": id, "cwd": "/tmp/" + project,
			"timestamp": at.UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": role, "content": text},
		}
		b, _ := json.Marshal(rec)
		return string(b)
	}
	body := line("user", userText) + "\n" + line("assistant", asstText) + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A project with more history than one digest can carry should not hear the
// same three sessions at every session start. It did: the candidate pool was
// three deep and the digest serves three, so everything on offer had just been
// served and the novelty ordering had nothing to promote. Measured on a seeded
// store of 300 sessions, five consecutive starts named 3 distinct sessions
// before this and 12 after.
func TestSessionStartSaysSomethingNewTheSecondTime(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	// Distinct subjects: the digest drops near-duplicates by fingerprint, and
	// nine variations on one sentence would be served as one.
	topics := []struct{ q, a string }{
		{"the invoice total rounds down by a cent", "decimal rather than float in the tax line settled it"},
		{"the nightly job crawls after midnight", "it re-read the whole table each pass; an index on created_at fixed it"},
		{"avatar upload rejects valid png files", "mime sniffing read only the first bytes; widening the check fixed it"},
		{"the search box loses focus while typing", "the list re-mounted on every keystroke; keying the rows fixed it"},
		{"csv export ends with a blank row", "the writer flushed a newline after the last record"},
		{"login redirects loop on safari only", "the cookie went out without SameSite=None and safari dropped it"},
		{"the webhook sender gives up too early", "three attempts with backoff drained the queue"},
		{"currency table is read on every request", "loading it once per process took the endpoint to a millisecond"},
		{"the settings header still says the old name", "renamed in the template and in the two tests that asserted it"},
	}
	for i, tp := range topics {
		seedClaudeAt(t, claude, "wide", fmt.Sprintf("session-%02d", i), tp.q, tp.a,
			now.Add(-time.Duration(i+1)*time.Hour))
	}
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}

	cwd := filepath.Join(os.TempDir(), "wide")
	first, n, _, _, _, firstIDs, _ := hookDigestResultFor(dir, cwd)
	if n == 0 || strings.TrimSpace(first) == "" {
		t.Fatal("the first session start served nothing to compare")
	}
	rememberInjectedIDsFor(dir, sessionStartKeyPrefix+"agent-one", hookProjectKey(cwd), firstIDs)

	_, _, _, _, _, secondIDs, _ := hookDigestResultFor(dir, cwd)
	if len(secondIDs) == 0 {
		t.Fatal("the second session start went quiet")
	}
	told := map[string]bool{}
	for _, id := range firstIDs {
		told[id] = true
	}
	fresh := 0
	for _, id := range secondIDs {
		if !told[id] {
			fresh++
		}
	}
	if fresh == 0 {
		t.Errorf("the second start repeated the first exactly: %v then %v", firstIDs, secondIDs)
	}
}
