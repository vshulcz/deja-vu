package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A bucket id names a local day, so notes written at one moment under two
// zones sit in two buckets and only a rebuild regroups them (#1058). What a
// rebuild cannot regroup is a bucket that arrived over sync: it was grouped on
// the peer, and counting it would rebuild the index on every `deja index`.
func TestNotesZoneDrift(t *testing.T) {
	moment := time.Date(2026, 7, 20, 23, 45, 0, 0, time.UTC)
	notes := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	if sources.NotesFile() != notes {
		t.Fatalf("notes file = %q, want %q", sources.NotesFile(), notes)
	}

	cases := []struct {
		name string
		meta SessionMeta
		want bool
	}{
		{"bucket grouped in this zone", SessionMeta{ID: "deja-2026-07-20-tz", Harness: "deja", Path: notes, Started: moment, Updated: moment}, false},
		{"bucket grouped east of here", SessionMeta{ID: "deja-2026-07-21-tz", Harness: "deja", Path: notes, Started: moment, Updated: moment}, true},
		{"same day, another machine's notes", SessionMeta{ID: "deja-2026-07-21-tz", Harness: "deja", Path: "/elsewhere/notes.jsonl", Started: moment, Updated: moment}, false},
		{"promoted note carries no day", SessionMeta{ID: sources.PromotedNoteID("claude:abc"), Harness: "deja", Path: notes, Started: moment, Updated: moment}, false},
		{"another harness that starts with deja-", SessionMeta{ID: "deja-2026-07-21-tz", Harness: "claude", Path: notes, Started: moment, Updated: moment}, false},
	}
	saved := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = saved })
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			m := Manifest{Version: version, Sessions: map[string]SessionMeta{c.meta.Harness + ":" + c.meta.ID: c.meta}}
			if err := writeManifest(dir, m); err != nil {
				t.Fatal(err)
			}
			if got := NotesZoneDrift(dir); got != c.want {
				t.Errorf("NotesZoneDrift = %v, want %v", got, c.want)
			}
		})
	}

	// No store yet is not drift: the first `deja index` has nothing to regroup.
	if NotesZoneDrift(t.TempDir()) {
		t.Error("NotesZoneDrift on a store with no manifest = true, want false")
	}
}
