package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var farFuture = time.Now().Add(100 * 365 * 24 * time.Hour)

// The week note says how many sessions their agent deleted this week, and the
// manifest knew only that a transcript was gone, not since when. The pass that
// first finds it gone stamps the time, and a rebuild carries the stamp rather
// than restarting it.
func TestKeptSinceDatesADeletedTranscriptFromThePassThatFoundItGone(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	before := time.Now().Add(-time.Second)
	if got, err := KeptSince(dir, before, farFuture); err != nil || len(got) != 0 {
		t.Fatalf("nothing is deleted yet, KeptSince = %v, %v", got, err)
	}
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	got, err := KeptSince(dir, before, farFuture)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "s1" || got[0].Harness != "claude" {
		t.Fatalf("KeptSince = %+v, want the deleted s1", got)
	}
	at := got[0].At
	if at.Before(before) || at.After(time.Now().Add(time.Second)) {
		t.Fatalf("kept at %v, want the pass that found it gone", at)
	}
	if later, _ := KeptSince(dir, time.Now().Add(time.Hour), farFuture); len(later) != 0 {
		t.Errorf("a window after the deletion still counts it: %+v", later)
	}
	if err = Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	again, err := KeptSince(dir, before, farFuture)
	if err != nil || len(again) != 1 || !again[0].At.Equal(at) {
		t.Fatalf("after a rebuild KeptSince = %+v, %v; want s1 still dated %v", again, err, at)
	}
}

// A rebuild can be the first pass to see the file gone, and it dates it too.
func TestKeptSinceDatesATranscriptARebuildFoundGone(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	before := time.Now().Add(-time.Second)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := KeptSince(dir, before, farFuture)
	if err != nil || len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("KeptSince = %+v, %v; want s1", got, err)
	}
}

// A row kept before the stamp existed has no date, and no window claims it.
func TestKeptSinceLeavesOutARowWithNoDate(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := m.Files[s1]
	f.KeptAt = 0
	m.Files[s1] = f
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	if got, _ := KeptSince(dir, time.Unix(0, 0), farFuture); len(got) != 0 {
		t.Errorf("an undated kept row was counted: %+v", got)
	}
}

// The stamp is whole seconds, so a pass in the second a test runs in proves
// nothing about carrying it. A row dated three days back keeps that date
// through a rebuild and through an incremental pass that rewrites the
// manifest.
func TestKeptSinceKeepsAnOlderDateThroughLaterPasses(t *testing.T) {
	dir, claudeRoot, s1, _ := seedTwoTranscripts(t)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-3 * 24 * time.Hour).Unix()
	setKeptAt(t, dir, s1, past)
	check := func(after string) {
		t.Helper()
		got, err := KeptSince(dir, time.Unix(0, 0), farFuture)
		if err != nil || len(got) != 1 || got[0].At.Unix() != past {
			t.Fatalf("after %s KeptSince = %+v, %v; want s1 dated %d", after, got, err, past)
		}
	}
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	check("a rebuild")
	s3 := filepath.Join(claudeRoot, "-Users-me-deja-vu", "s3.jsonl")
	if err := os.WriteFile(s3, []byte(`{"type":"user","sessionId":"s3","timestamp":"2026-09-02T03:04:05Z","message":{"role":"user","content":"the frobnic cache warmed"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	check("an incremental pass")
}

// A store written before deja dated deletions holds rows for transcripts
// already gone and not marked kept: 0.21 had no Kept at all. The first pass
// of this build finds them gone, and dating them now would report months of
// deletions as this week's. They stay undated; a deletion after that pass is
// dated.
func TestKeptSinceLeavesOutWhatAnOlderStoreLostBeforeIt(t *testing.T) {
	for _, force := range []bool{false, true} {
		dir, _, s1, s2 := seedTwoTranscripts(t)
		if err := os.Remove(s1); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		m.KeptDated = false
		if err := writeManifestOnly(dir, m); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "claude", force, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := KeptSince(dir, time.Unix(0, 0), farFuture); len(got) != 0 {
			t.Fatalf("force=%v: a deletion from before the upgrade was dated: %+v", force, got)
		}
		if got := sessionsFor(t, dir, "zorblax"); len(got) != 1 {
			t.Fatalf("force=%v: the undated deletion was not kept: %v", force, got)
		}
		if err := os.Remove(s2); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "claude", false, nil); err != nil {
			t.Fatal(err)
		}
		got, _ := KeptSince(dir, time.Unix(0, 0), farFuture)
		if len(got) != 1 || got[0].ID != "s2" {
			t.Fatalf("force=%v: a deletion after the upgrade pass, KeptSince = %+v; want s2", force, got)
		}
	}
}

// The window is whole seconds, from inclusive and to exclusive, so a stamp in
// the second one note is written falls in exactly one window.
func TestKeptSinceWindowsMeetWithoutAGapOrOverlap(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).Unix()
	setKeptAt(t, dir, s1, at)
	edge := time.Unix(at, 0)
	first, _ := KeptSince(dir, edge.Add(-time.Hour), edge)
	second, _ := KeptSince(dir, edge, edge.Add(time.Hour))
	if len(first)+len(second) != 1 || len(second) != 1 {
		t.Errorf("a stamp on the boundary: first window %d, second %d; want it once, in the second", len(first), len(second))
	}
}

func setKeptAt(t *testing.T, dir, path string, at int64) {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := m.Files[path]
	if !f.Kept {
		t.Fatalf("%s is not kept: %+v", path, f)
	}
	f.KeptAt = at
	m.Files[path] = f
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
}

// An upgraded store with nothing new on disk reads as up to date, and the
// marker that starts dating was never written: the first deletion after the
// upgrade went undated. The pass that finds the marker missing writes it.
func TestKeptSinceDatesTheFirstDeletionAfterAnUpgradeWithNothingNew(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.KeptDated = false
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := KeptSince(dir, time.Unix(0, 0), farFuture); len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("the first deletion after the upgrade, KeptSince = %+v; want s1", got)
	}
}
