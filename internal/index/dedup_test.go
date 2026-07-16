package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// The same gemini chat can exist as both session-x.json and session-x.jsonl.
// Full rebuilds and — the harder case — incremental updates that touch both
// files in one pass must not write the shared messages twice. Distinct
// messages under one session key (codex history style) must still merge.
func TestDuplicateFormatSessionsDoNotDuplicateMessages(t *testing.T) {
	tmp := t.TempDir()
	gem := filepath.Join(tmp, "gemini")
	chats := filepath.Join(gem, "tmp", "s", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON := func() {
		doc := `{"sessionId":"dup1","startTime":"2026-07-15T10:00:00.000Z","lastUpdated":"2026-07-15T10:00:00.000Z","messages":[{"id":"m1","timestamp":"2026-07-15T10:00:01.000Z","type":"user","content":"dupneedle question"}]}`
		if err := os.WriteFile(filepath.Join(chats, "session-dup.json"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON()
	jsonl := filepath.Join(chats, "session-dup.jsonl")
	head := `{"sessionId":"dup1","startTime":"2026-07-15T10:00:00.000Z","lastUpdated":"2026-07-15T10:05:00.000Z"}` + "\n" +
		`{"id":"m1","timestamp":"2026-07-15T10:00:01.000Z","type":"user","content":"dupneedle question"}` + "\n"
	if err := os.WriteFile(jsonl, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_GEMINI_ROOT", gem)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "nc"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "nx"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "n.db"))
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "dupneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	count := func(what string) int {
		ss, err := Search(dir, search.Options{Query: what, All: true})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range ss {
			n += len(s.Messages)
		}
		return n
	}
	if got := count("dupneedle question"); got != 1 {
		t.Fatalf("after rebuild: %d copies, want 1", got)
	}

	// Touch both format files in one incremental pass.
	f, err := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"m2","timestamp":"2026-07-15T10:06:00.000Z","type":"user","content":"dupneedle again"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeJSON() // rewrite -> mtime change on the twin
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := count("dupneedle question"); got != 1 {
		t.Fatalf("after dual-file incremental: %d copies, want 1", got)
	}
	if got := count("dupneedle again"); got != 1 {
		t.Fatalf("new message: %d copies, want 1", got)
	}
}
