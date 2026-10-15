package index

import (
	"os"
	"testing"
	"time"
)

// The week note says how many sessions their agent deleted this week, and the
// manifest knew only that a transcript was gone, not since when. The pass that
// first finds it gone stamps the time, and a rebuild carries the stamp rather
// than restarting it.
func TestKeptSinceDatesADeletedTranscriptFromThePassThatFoundItGone(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	before := time.Now().Add(-time.Second)
	if got, err := KeptSince(dir, before); err != nil || len(got) != 0 {
		t.Fatalf("nothing is deleted yet, KeptSince = %v, %v", got, err)
	}
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	got, err := KeptSince(dir, before)
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
	if later, _ := KeptSince(dir, time.Now().Add(time.Hour)); len(later) != 0 {
		t.Errorf("a window after the deletion still counts it: %+v", later)
	}
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	again, err := KeptSince(dir, before)
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
	got, err := KeptSince(dir, before)
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
	if got, _ := KeptSince(dir, time.Unix(0, 0)); len(got) != 0 {
		t.Errorf("an undated kept row was counted: %+v", got)
	}
}
