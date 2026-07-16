package index

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// One unreadable source file must not fail the whole index update: the file
// is skipped for the pass, its old records survive, and it is retried once
// readable again.
func TestUnreadableSourceSkippedNotFatal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0 does not make files unreadable on windows")
	}
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-grace")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(proj, "good.jsonl")
	bad := filepath.Join(proj, "bad.jsonl")
	line := func(sid, text string) string {
		return `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(good, []byte(line("g1", "goodneedle stays")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(line("b1", "badneedle original")), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "badneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}

	// Rewrite bad.jsonl shorter (forces the non-append replace path) and make
	// it unreadable, then also shrink good.jsonl so the same pass has real work.
	if err := os.WriteFile(bad, []byte(line("b1", "badneedle updated")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(bad, 0o644) }()
	if err := os.WriteFile(good, []byte(line("g1", "goodneedle v2")), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureForSearch(dir, search.Options{Query: "goodneedle"}, false, nil); err != nil {
		t.Fatalf("update failed instead of skipping unreadable file: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "goodneedle", All: true}); err != nil || len(ss) != 1 || ss[0].Messages[0].Text != "goodneedle v2" {
		t.Fatalf("good file not reindexed: %#v err=%v", ss, err)
	}
	// old records of the unreadable file survive
	if ss, err := Search(dir, search.Options{Query: "badneedle", All: true}); err != nil || len(ss) != 1 || ss[0].Messages[0].Text != "badneedle original" {
		t.Fatalf("old records lost for skipped file: %#v err=%v", ss, err)
	}

	// readable again → retried and updated
	if err := os.Chmod(bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "badneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, search.Options{Query: "badneedle", All: true}); err != nil || len(ss) != 1 || ss[0].Messages[0].Text != "badneedle updated" {
		t.Fatalf("skipped file not retried: %#v err=%v", ss, err)
	}
}
