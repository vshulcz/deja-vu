package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Resume asks the store for the row itself (#4205): every session either
// table holds counts, whatever the projection would read from it.
func TestOpencodeStoreLacksAsksForTheRow(t *testing.T) {
	db := opencodeMixedFixture(t)
	t.Setenv("DEJA_OPENCODE_DB", db)
	for _, id := range []string{"old", "moved", "new"} {
		if OpencodeStoreLacks("opencode", id) {
			t.Errorf("%s is in the store and was called deleted", id)
		}
	}
	if !OpencodeStoreLacks("opencode", "gone") {
		t.Error("an id in neither table was not called deleted")
	}
	if !OpencodeStoreLacks("opencode", "it's") {
		t.Error("a quoted id broke the lookup")
	}
	// A session with no turns is still opencode's.
	if out, err := exec.Command("sqlite3", db, `insert into session_v2 values('empty','p1',null,'/w','',1,1)`).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if OpencodeStoreLacks("opencode", "empty") {
		t.Error("an empty session was called deleted")
	}

	// Kilo's CLI database is asked the same way.
	t.Setenv("DEJA_KILO_DB", db)
	if OpencodeStoreLacks("kilocode", "new") || !OpencodeStoreLacks("kilocode", "gone") {
		t.Error("the Kilo store was not the one asked")
	}
	// No store: nothing to go on.
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "missing.db"))
	if OpencodeStoreLacks("opencode", "gone") {
		t.Error("refused with no store to ask")
	}
}
