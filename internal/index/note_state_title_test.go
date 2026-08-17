package index

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A promoted note's title carries its state, and the loader rewrites it on
// every correction. The incremental path only ever derived a title for a
// session it had not seen, so the state froze at whatever the note held when
// it was first indexed: after `promote --state rejected` the store still said
// "[accepted]" on `deja last`, in the digest, and in the citation line the
// hook pre-writes for the agent to say aloud. Only a full rebuild caught up
// (#R11).
func TestPromotedNoteTitleTracksTheCorrection(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	when := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	const src = "claude:sess1"

	if err := sources.AppendPromotedSourced("work/api", "what pool size should the pool use",
		"pool size 200", src, "accepted", nil, when, when); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := sources.AppendPromotedSourced("work/api", "what pool size should the pool use",
		"rolled back, the pool stays at 20", src, "rejected", nil, when, when.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Incremental, not a rebuild: the rebuild was never the broken path.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := sources.PromotedNoteID(src)
	for _, m := range metas {
		if m.ID != id {
			continue
		}
		if strings.Contains(m.Title, "[accepted]") {
			t.Fatalf("the note still names the state the correction reversed: %q", m.Title)
		}
		if !strings.Contains(m.Title, "[rejected]") {
			t.Fatalf("the note's title lost its state: %q", m.Title)
		}
		return
	}
	t.Fatalf("no note %q in %d sessions", id, len(metas))
}
