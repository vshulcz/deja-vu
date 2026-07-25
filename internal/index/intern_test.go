package index

import (
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// Records intern their key and source path into a table stored in the
// manifest. The table is append-only, so a log that grows must keep the ids
// it already handed out: restarting the table would give id 0 to a new string
// and silently repoint every record already using it — a message would come
// back attributed to a different session.
func TestInternedIDsSurviveIncrementalAppend(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(id, ts, text string) string {
		return `{"type":"user","sessionId":"` + id + `","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	// Two sessions up front, so the table already holds s1's strings at the
	// low ids.
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"),
		[]byte(line("s1", "2026-01-02T03:04:05Z", "alpha first message")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s2.jsonl"),
		[]byte(line("s2", "2026-01-02T04:04:05Z", "bravo first message")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Grow ONLY the second session. A restarted table would intern s2's key
	// as id 0 — the id s1 already owns — and s1's existing records would come
	// back attributed to s2. Adding a file instead would force a full rebuild
	// and never exercise the append path.
	if err := os.WriteFile(filepath.Join(proj, "s2.jsonl"),
		[]byte(line("s2", "2026-01-02T04:04:05Z", "bravo first message")+
			line("s2", "2026-01-02T04:05:05Z", "bravo second message")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// Every record must still name the session it belongs to.
	tbl, err := loadRecordTables(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	byText := map[string]string{}
	if err := eachRecord(filepath.Join(dir, "records.bin"), tbl, func(r Record) {
		byText[r.Text] = r.Key
		if _, ok := m.Sessions[r.Key]; !ok {
			t.Errorf("record %q resolved to key %q, which is not a session", r.Text, r.Key)
		}
		if r.SourcePath == "" {
			t.Errorf("record %q lost its source path", r.Text)
		}
	}); err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]string{
		"alpha first message":  "claude:s1",
		"bravo first message":  "claude:s2",
		"bravo second message": "claude:s2",
	} {
		if got := byText[text]; got != want {
			t.Errorf("record %q attributed to %q, want %q", text, got, want)
		}
	}
	// And the whole thing is still searchable per session.
	for _, c := range []struct{ q, id string }{{"alpha first message", "s1"}, {"bravo second message", "s2"}} {
		ss, err := Search(dir, search.Options{Query: c.q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != c.id {
			t.Errorf("search %q returned %d sessions, want just %s", c.q, len(ss), c.id)
		}
	}
}

// The table only ever grows, so ids already in use keep their meaning.
func TestInternedIDsAreStable(t *testing.T) {
	tbl := newRecordTables()
	a := tbl.intern("claude:one")
	b := tbl.intern("/path/one.jsonl")
	if a == b {
		t.Fatal("distinct strings shared an id")
	}
	if again := tbl.intern("claude:one"); again != a {
		t.Fatalf("id changed on re-intern: %d then %d", a, again)
	}
	// A reader rebuilt from the persisted slice resolves the same way.
	rt := tablesFromStrings(tbl.strs)
	if rt.lookup(a) != "claude:one" || rt.lookup(b) != "/path/one.jsonl" {
		t.Fatalf("round trip lost ids: %q %q", rt.lookup(a), rt.lookup(b))
	}
	// Continuing a persisted table must not reuse an id.
	grown := tablesFromStrings(tbl.strs)
	c := grown.intern("claude:two")
	if c == a || c == b {
		t.Fatalf("new string reused id %d", c)
	}
	if grown.lookup(a) != "claude:one" {
		t.Fatal("growing the table repointed an existing id")
	}
	// An unknown id resolves to empty rather than panicking or aliasing.
	if got := rt.lookup(uint64(len(tbl.strs)) + 5); got != "" {
		t.Fatalf("out-of-range id resolved to %q", got)
	}
}

// The catalog must be rebuilt when a bucket changes, or a corrupt bucket goes
// unreported and the ladder never triggers a rebuild.
func TestCatalogCacheNoticesBucketChanges(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"),
		[]byte(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"quetzalcoatl marker"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	first, err := tokenCatalogCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !first["quetzalcoatl"] {
		t.Fatalf("catalog missing the indexed token")
	}
	if again, err := tokenCatalogCached(dir); err != nil || len(again) != len(first) {
		t.Fatalf("second call = %d tokens err=%v", len(again), err)
	}
	// Corrupting a bucket must invalidate the cache and surface as an error,
	// which is what makes the search ladder rebuild a damaged index.
	if err := os.WriteFile(filepath.Join(dir, "buckets", "zz.bin"), []byte("not a bucket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenCatalogCached(dir); err == nil {
		t.Fatal("a corrupt bucket was hidden by the cached catalog")
	}
}
