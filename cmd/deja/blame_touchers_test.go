package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// blame picks candidates with an ordinary search for the file's stem, and the
// paths a tool call recorded do not answer one — so the session that actually
// changed the file was invisible while an older one that only mentioned it in
// passing was the answer (#688).
func TestBlameFindsSessionsThatOnlyTouchedTheFile(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(name string, lines ...string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(path string) string {
		b, _ := json.Marshal(map[string]any{"file_path": path, "old_string": "a", "new_string": "b"})
		return `{"type":"assistant","sessionId":"edited","cwd":"/w/p","timestamp":"2026-07-25T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":` + string(b) + `}]}}`
	}
	// Says the name, never touches it.
	write("named.jsonl",
		`{"type":"user","sessionId":"named","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"we rewrote pool.go to fix the starvation"}}`)
	// Touches it, never says the name.
	write("edited.jsonl",
		`{"type":"user","sessionId":"edited","cwd":"/w/p","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","content":"the starvation is back under burst load"}}`,
		edit(filepath.ToSlash(filepath.Join(tmp, "repo", "pool.go"))))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	target, err := search.ResolveBlamePath("pool.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := findBlameHits(dir, target, search.BlameOptions{All: true}, "search", nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.Session.ID] = true
	}
	if !ids["edited"] {
		t.Errorf("the session that edited the file is missing: %v", ids)
	}
	if !ids["named"] {
		t.Errorf("the session that named the file is missing: %v", ids)
	}
	// Newer work on the same file comes first — that is the question blame answers.
	if len(hits) > 0 && hits[0].Session.ID != "edited" {
		t.Errorf("top hit = %q, want the session that actually changed it", hits[0].Session.ID)
	}
}

// A blame for a file nobody touched must not drag in unrelated sessions.
func TestBlameTouchersMatchTheWholeName(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"e1","cwd":"/w/p","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","content":"unrelated work"}}` + "\n" +
		`{"type":"assistant","sessionId":"e1","cwd":"/w/p","timestamp":"2026-07-25T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/poolmanager.go","old_string":"a","new_string":"b"}}]}}` + "\n" +
		// A path that *contains* the name is not the file: old-pool.go and
		// pool.go are two files, and substring matching answers for both.
		`{"type":"assistant","sessionId":"e1","cwd":"/w/p","timestamp":"2026-07-25T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/old-pool.go","old_string":"a","new_string":"b"}}]}}`
	if err := os.WriteFile(filepath.Join(root, "e1.jsonl"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	target, err := search.ResolveBlamePath("pool.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := findBlameHits(dir, target, search.BlameOptions{All: true}, "search", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("poolmanager.go answered a blame for pool.go: %#v", hits)
	}
}
