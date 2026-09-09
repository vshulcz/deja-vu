package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A session still being written is appendable, and an append blocks the
// answer: a search for last month's history sat through the tail of a live
// transcript first — seconds per query, scaling with the file rather than with
// the question (#3021). Past the cap the tail goes to the detached warmup, the
// way rewrite-grade work already does.
func TestASearchDoesNotWaitOnALongLiveSession(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "live.jsonl")
	turn := func(n, text string) string {
		return `{"type":"user","sessionId":"live","cwd":"/tmp/app","timestamp":"2026-01-0` + n +
			`T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(turn("1", "why does the quokkabloom retry")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// A small append is still read before answering: the wait is the point of
	// having a current index at all.
	small, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := small.WriteString(turn("2", "the retry is capped at three")); err != nil {
		t.Fatal(err)
	}
	_ = small.Close()
	stale, err := EnsureForSearchStale(dir, query.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Error("an ordinary turn was handed to the warmup instead of being read")
	}

	// And a session that has been writing all day is not something every
	// search waits on.
	inlineAppendMax = 1 << 10
	t.Cleanup(func() { inlineAppendMax = 8 << 20 })
	big, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := big.WriteString(turn("3", strings.Repeat("tool output ", 400))); err != nil {
		t.Fatal(err)
	}
	_ = big.Close()
	stale, err = EnsureForSearchStale(dir, query.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Error("the search waited on the tail of a live session")
	}
}
