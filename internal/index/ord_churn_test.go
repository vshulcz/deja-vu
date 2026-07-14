package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A rewritten session file goes through the non-append update path as a
// replacement. Its postings must carry the same Ord the manifest stores,
// otherwise cutPostingsBySession drops them and the session becomes
// unsearchable.
func TestReplacedSessionStaysSearchable(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-ordchurn")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := `{"type":"user","sessionId":"ord1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"oldneedle question first"}}` + "\n"
	s2 := `{"type":"user","sessionId":"ord2","timestamp":"2026-01-02T04:04:05Z","message":{"role":"user","content":"second session text"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "ord1.jsonl"), []byte(s1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "ord2.jsonl"), []byte(s2), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "oldneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}

	// Rewrite ord1 shorter (same session, smaller file, so canAppendIncremental
	// rejects it and the non-append replacement path runs) while ord2 keeps its
	// Ord — a fresh nextSessionOrd for ord1 would no longer match its postings.
	s1b := `{"type":"user","sessionId":"ord1","timestamp":"2026-01-02T05:04:05Z","message":{"role":"user","content":"newneedle"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "ord1.jsonl"), []byte(s1b), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "newneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "newneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "ord1" || len(ss[0].Messages) != 1 {
		t.Fatalf("replaced session not searchable: %#v", ss)
	}
	// Manifest Ord and posting Sid must agree.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:ord1"]
	if !ok {
		t.Fatal("no meta for claude:ord1")
	}
	posts, err := postingsFor(dir, "tnewneedle")
	if err != nil || len(posts) == 0 {
		t.Fatalf("postings err=%v n=%d", err, len(posts))
	}
	for _, p := range posts {
		if p.Sid != meta.Ord {
			t.Fatalf("posting Sid=%d != manifest Ord=%d", p.Sid, meta.Ord)
		}
	}
}
