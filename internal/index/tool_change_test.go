package index

import (
	"os"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A store skipped because an external CLI was missing is not stale by its file
// state — the transcripts did not change — so the next run said "index is up to
// date" and the store stayed out of recall, after doctor had told the user to
// install the tool and run exactly that (#1760).
func TestInstallingTheMissingToolMakesTheIndexStale(t *testing.T) {
	without := toolFingerprint(false, false)
	sqliteOnly := toolFingerprint(true, false)
	both := toolFingerprint(true, true)
	if without == sqliteOnly || sqliteOnly == both || without == both {
		t.Fatalf("the fingerprint does not separate the three states: %q %q %q", without, sqliteOnly, both)
	}

	m := Manifest{Version: version, Files: map[string]FileState{}, ToolFingerprint: without}
	if manifestFresh(m, map[string]FileState{}, "") {
		t.Error("an index built without the tools is current on a machine that now has them")
	}
	m.ToolFingerprint = toolFingerprint(sources.SQLite3Available(), sources.ZstdAvailable())
	if !manifestFresh(m, map[string]FileState{}, "") {
		t.Error("an index built with today's tools is not current")
	}

	// An index from before this field existed is not rebuilt for it: an empty
	// fingerprint says nothing was recorded, not that the tools were absent.
	m.ToolFingerprint = ""
	if !manifestFresh(m, map[string]FileState{}, "") {
		t.Error("an index built by an older deja is rebuilt for a field it never wrote")
	}
}

// A hook runs with whatever PATH its harness has, which is often minimal. If
// losing sight of a tool counted as a change, that hook and a terminal with
// both tools would trade full rebuilds forever; and if the hook's build erased
// what the terminal knew, the next terminal run would read it as a tool newly
// gained and rebuild again.
func TestAToolLostDoesNotStartARebuildLoop(t *testing.T) {
	both := toolFingerprint(true, true)
	none := toolFingerprint(false, false)

	t.Setenv("PATH", "")
	if toolsChanged(Manifest{ToolFingerprint: both}) {
		t.Error("losing a tool counts as a change, so a minimal-PATH hook forces a rebuild")
	}
	if got := mergedToolFingerprint(both); got != both {
		t.Errorf("a build with no tools erased what the index knew: %q", got)
	}
	if !toolsChanged(Manifest{ToolFingerprint: none}) == false {
		t.Error("with no tools at all, nothing is newly readable")
	}
	if got := mergedToolFingerprint(""); got != none {
		t.Errorf("a first build with no tools recorded %q", got)
	}
}

// An index created by `deja sync import` records the tools too: a blank
// fingerprint reads as "written by an older deja", and the store skipped for a
// missing CLI would never be re-read once it was installed.
func TestAnImportedIndexRecordsTheTools(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.ToolFingerprint == "" {
		t.Error("an index built by sync import records no tools, so installing one never re-reads a skipped store")
	}
}
