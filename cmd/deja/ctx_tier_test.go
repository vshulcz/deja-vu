package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// ctx is the command the hook names to an agent, and agents ask in sentences.
// Retrieval already answers those: it drops the terms nothing carries and
// hands back the session under the close tier. ctx threw that away — it fed
// the sessions to search.Run with the tier and the variant map missing, so
// scoring looked for the whole literal query, found no turn holding it and
// returned "no session matches" for a store that plainly does. `deja search`
// on the identical words returned the session (#R8).
func TestCtxAnswersASentenceLikeSearchDoes(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the http client hammered the server on failure"},"timestamp":"2026-08-04T10:00:00Z","sessionId":"w81","cwd":"/proj"}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"we decided to cap retries at 3 with exponential backoff"}]},"timestamp":"2026-08-04T10:01:00Z","sessionId":"w81","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	const q = "add retries to the http client"
	search, err := captureRun(t, "search", q)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search, "http client hammered") {
		t.Fatalf("search itself lost the session, the premise is gone:\n%s", search)
	}

	out, err := captureRun(t, "ctx", q)
	if err != nil {
		t.Fatalf("ctx refused a question search answers: %v", err)
	}
	if !strings.Contains(out, "cap retries at 3") {
		t.Errorf("ctx answered without the decision:\n%s", out)
	}
}
