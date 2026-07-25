package index

import (
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// Ord is the posting's session id. A new session takes max(Ord)+1 over the
// manifest being built, but a re-parsed session takes its Ord from the OLD
// manifest — which the new map has not seen yet. When every existing file
// changes at once, the new session is handed an Ord an existing session is
// about to reclaim, and the two sessions' postings merge: one of them stops
// being findable by its own words.
func TestOrdsStayUniqueWhenEveryFileChangesAtOnce(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(file, id, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, file), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("s1.jsonl", "s1", "alpha original content")
	write("s2.jsonl", "s2", "bravo original content")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Every existing file grows, and a new session appears whose file sorts
	// ahead of them — so it is assigned an Ord before the old ones reclaim
	// theirs.
	write("s1.jsonl", "s1", "alpha original content plus quetzalcoatl marker")
	write("s2.jsonl", "s2", "bravo original content plus xochipilli marker")
	write("aaa.jsonl", "s3", "charlie brandnew tlaloc marker")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	byOrd := map[uint32]string{}
	for key, meta := range m.Sessions {
		if other, dup := byOrd[meta.Ord]; dup {
			t.Fatalf("sessions %q and %q share Ord %d — their postings are merged", other, key, meta.Ord)
		}
		byOrd[meta.Ord] = key
	}
	// And the behavior that Ord uniqueness protects: each session is findable
	// by the marker only it contains.
	for _, c := range []struct{ marker, id string }{
		{"quetzalcoatl", "s1"},
		{"xochipilli", "s2"},
		{"tlaloc", "s3"},
	} {
		got, err := Search(dir, search.Options{Query: c.marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != c.id {
			ids := []string{}
			for _, s := range got {
				ids = append(ids, s.ID)
			}
			t.Errorf("search %q returned %v, want exactly [%s]", c.marker, ids, c.id)
		}
	}
}
