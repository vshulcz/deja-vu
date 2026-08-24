package index

import (
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
