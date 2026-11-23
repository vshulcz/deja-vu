package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// #6162: a prompt hook with no transcript_path finds the live session where
// the index last saw it, and never lists the harness's store to look for it.

func liveTranscriptText(msgs []model.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role + ": " + m.Text + "\n")
	}
	return b.String()
}

func TestLiveTranscriptWithoutPathIsReadWhereTheIndexSawIt(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CODEX_ROOT")
	id := "019d0001-6162-7000-8000-000000000001"
	writeCodexRollout(t, root, id, time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
		"keep retries at three", "decision: cap retries at three")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	if got := indexedTranscriptPath(dir, "codex", id); got == "" {
		t.Fatal("indexedTranscriptPath found no file for an indexed Codex session")
	}
	msgs := readLiveHookTranscriptMessages(dir, promptHookInput{Harness: "codex", SessionID: id}, "/tmp/app", "/tmp/app")
	if text := liveTranscriptText(msgs); !strings.Contains(text, "cap retries at three") {
		t.Fatalf("live transcript without a path = %q, want the indexed session's turns", text)
	}
}

func TestLiveTranscriptWithoutPathDoesNotSearchTheStore(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CODEX_ROOT")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	// Written after the index was built: only a search of the store finds it,
	// and the file names the id, so the old lookup did.
	id := "019d0002-6162-7000-8000-000000000002"
	writeCodexRollout(t, root, id, time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC),
		"newer than the index", "decision: not looked up on the prompt path")
	msgs := readLiveHookTranscriptMessages(dir, promptHookInput{Harness: "codex", SessionID: id}, "/tmp/app", "/tmp/app")
	if len(msgs) != 0 {
		t.Fatalf("live transcript of an unindexed session = %q, want none: the store is not searched on the prompt path", liveTranscriptText(msgs))
	}
	// PreCompact still finds it by id.
	tr, reason, _ := compactionSource(precompactHookInput{Harness: "codex", SessionID: id}, "")
	if reason != "" || !strings.Contains(liveTranscriptText(tr.Session.Messages), "not looked up") {
		t.Fatalf("compactionSource by id: reason %q, messages %q; want the session", reason, liveTranscriptText(tr.Session.Messages))
	}
}

func TestIndexedTranscriptPathRefusesAFileThatIsGone(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CODEX_ROOT")
	id := "019d0003-6162-7000-8000-000000000003"
	writeCodexRollout(t, root, id, time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC), "q", "a")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	path := indexedTranscriptPath(dir, "codex", id)
	if path == "" {
		t.Fatal("indexedTranscriptPath found no file for an indexed Codex session")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := indexedTranscriptPath(dir, "codex", id); got != "" {
		t.Fatalf("indexedTranscriptPath = %q for a deleted file, want \"\"", got)
	}
	for _, c := range []struct{ harness, id string }{{"", id}, {"codex", ""}, {"claude", id}} {
		if got := indexedTranscriptPath(dir, c.harness, c.id); got != "" {
			t.Errorf("indexedTranscriptPath(%q, %q) = %q, want \"\"", c.harness, c.id, got)
		}
	}
	if got := indexedTranscriptPath(filepath.Join(t.TempDir(), "none"), "codex", id); got != "" {
		t.Errorf("indexedTranscriptPath with no index = %q, want \"\"", got)
	}
}
