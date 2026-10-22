package index

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// One changed file that could not be appended sent every changed file in the
// pass down the replacement path, which parses each from byte zero: a 1.27 GB
// Claude transcript that grew 281 KB was read whole because two other
// transcripts kept their size (#5324). Decided per file now: the grown one is
// appended from its offset, and only the rewritten one is re-read.
//
// The progress lines name the path each file took: the append path says what
// it updated, the replacement path how many transcripts it re-read.
func TestAMixedBatchAppendsWhatItCanAndReplacesTheRest(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	// Same length, different bytes: a real rewrite, which must still be read.
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the queue warms on boot"))
	bumpMTime(t, same)

	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	got := progress.String()
	if !strings.Contains(got, "updated 1 file (1 new message)") {
		t.Errorf("the grown transcript was not appended from its offset: %q", got)
	}
	if !strings.Contains(got, replacementPassMarker+" 1 changed transcript\n") &&
		!strings.Contains(got, replacementPassMarker+" 1 changed transcript,") {
		t.Errorf("the replacement path read more than the rewritten file: %q", got)
	}

	for query, want := range map[string]string{
		"billing webhook retries": "s-grown",
		"exporter retries":        "s-grown",
		"queue warms":             "s-same",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
	if ss, _ := Search(dir, search.Options{Query: "cache warms", All: true}); len(ss) != 0 {
		t.Errorf("the rewritten file kept its old text: %#v", ss)
	}
	if ok, _ := UpToDate(dir, ""); !ok {
		t.Error("the index is not current after the pass")
	}
}

// A file whose mtime moved and whose bytes did not has nothing to read. It is
// recorded with its new state, so it neither forces a re-read nor keeps the
// index stale for the next pass (#5324).
func TestATouchedTranscriptIsRecordedNotReread(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot")+
		claudeLine("s-same", "2026-01-02T03:04:06Z", "token ghp_abcdefghijklmnop0123456789QRSTUV"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	before, err := Redactions(dir)
	if err != nil || before.Files[same] == 0 {
		t.Fatalf("control: the planted secret was not counted: %#v %v", before, err)
	}

	bumpMTime(t, same)
	if ok, _ := UpToDate(dir, ""); ok {
		t.Fatal("control: a new mtime should make the index look stale before the pass")
	}
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, replacementPassMarker) || strings.Contains(got, "updated") {
		t.Errorf("a touched transcript was read again: %q", got)
	}
	if ok, _ := UpToDate(dir, ""); !ok {
		t.Error("the new mtime was not recorded, so every pass sees the file as changed")
	}
	// What the read derived is still the index's: recording the mtime is not
	// a reason to forget the file's redactions.
	if after, err := Redactions(dir); err != nil || after.Files[same] != before.Files[same] {
		t.Errorf("redactions for the touched file: %d, was %d (%v)", after.Files[same], before.Files[same], err)
	}

	// Touched beside a grown file: the pass is a plain append.
	bumpMTime(t, same)
	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	progress.Reset()
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, replacementPassMarker) || !strings.Contains(got, "updated 1 file") {
		t.Errorf("a touched file sent the batch to replacement: %q", got)
	}
	ss, err := Search(dir, search.Options{Query: "cache warms", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s-same" {
		t.Fatalf("the touched file's session went missing: %#v", ss)
	}
}

// A transcript deleted in the same pass is kept, and the kept mark the first
// half writes into the walk must not reach the second half as a file put back.
func TestAMixedBatchKeepsADeletedTranscript(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	gone := filepath.Join(claude, "p", "gone.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot"))
	write(t, gone, claudeLine("s-gone", "2026-01-02T03:04:05Z", "the lockfile drifts nightly"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the queue warms on boot"))
	bumpMTime(t, same)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	got := progress.String()
	if !strings.Contains(got, "updated 1 file") || !strings.Contains(got, "no longer on disk") {
		t.Errorf("progress: %q", got)
	}
	if strings.Contains(got, "skipping") {
		t.Errorf("the second half read the deleted file: %q", got)
	}
	for query, want := range map[string]string{
		"lockfile drifts": "s-gone",
		"billing webhook": "s-grown",
		"queue warms":     "s-same",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Files[gone].Kept {
		t.Errorf("the deleted transcript lost its kept mark: %#v", m.Files[gone])
	}
}

func bumpMTime(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	next := fi.ModTime().Add(3 * time.Second)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
}

// Two transcripts that hold one session are reconciled only when they are
// read together. Split, the grown one was appended and the other replaced on
// its own, and the replacement wrote the grown file's turn a second time
// (#5358 is the multi-pass form). Each case ends where a rebuild would.
func TestAMixedBatchSharingASessionMatchesRebuild(t *testing.T) {
	const t0, t1, t2 = "2026-01-02T03:04:05Z", "2026-01-02T03:04:06Z", "2026-01-02T04:00:00Z"
	cases := []struct {
		name, id string
		setup    func(t *testing.T, a, b, c string)
		change   func(t *testing.T, a, b, c string)
	}{
		{
			name: "grown and rewritten",
			id:   "s-x",
			setup: func(t *testing.T, a, b, c string) {
				write(t, a, claudeLine("s-x", t0, "alpha one"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
			},
			change: func(t *testing.T, a, b, c string) {
				appendTo(t, a, claudeLine("s-x", t2, "alpha three"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta queue"))
				bumpMTime(t, b)
			},
		},
		{
			name: "grown and a torn line finished",
			id:   "s-x",
			setup: func(t *testing.T, a, b, c string) {
				write(t, a, claudeLine("s-x", t0, "alpha one"))
				line := claudeLine("s-x", t1, "beta cache")
				write(t, b, claudeLine("s-x", t0, "alpha one")+line[:len(line)-10])
			},
			change: func(t *testing.T, a, b, c string) {
				appendTo(t, a, claudeLine("s-x", t2, "alpha three"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
				bumpMTime(t, b)
			},
		},
		{
			name: "new copy beside a rewrite",
			id:   "s-b",
			setup: func(t *testing.T, a, b, c string) {
				write(t, a, claudeLine("s-a", t0, "alpha one"))
				write(t, b, claudeLine("s-b", t0, "bravo two"))
			},
			change: func(t *testing.T, a, b, c string) {
				write(t, c, claudeLine("s-b", t0, "bravo two")+claudeLine("s-b", t2, "charlie four"))
				write(t, b, claudeLine("s-b", t0, "bravo too"))
				bumpMTime(t, b)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			claude := os.Getenv("DEJA_CLAUDE_ROOT")
			a := filepath.Join(claude, "p", "a.jsonl")
			b := filepath.Join(claude, "p", "b.jsonl")
			c := filepath.Join(claude, "p", "c.jsonl")
			tc.setup(t, a, b, c)
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			tc.change(t, a, b, c)
			indexPass(t, dir)
			sessionMatchesRebuild(t, dir, "claude", tc.id)
		})
	}
}

// sessionMatchesRebuild is matchesRebuild with the messages compared as a set: the
// order two files' turns interleave in follows the order they are read, which
// neither path fixes.
func sessionMatchesRebuild(t *testing.T, dir, harness, id string) {
	t.Helper()
	fresh := filepath.Join(t.TempDir(), "rebuild.db")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	sorted := func(v string) string {
		lines := strings.Split(v, "\n")
		slices.Sort(lines)
		return strings.Join(lines, "\n")
	}
	got, want := sessionView(t, dir, harness, id)+rowView(t, dir, harness, id), sessionView(t, fresh, harness, id)+rowView(t, fresh, harness, id)
	if sorted(got) != sorted(want) {
		t.Errorf("%s:%s after incremental passes:\n%s\nafter a rebuild:\n%s", harness, id, got, want)
	}
}

// The control: distinct sessions still split, and still match a rebuild.
func TestAMixedBatchOfDistinctSessionsSplitsAndMatchesRebuild(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(claude, "p", "a.jsonl")
	b := filepath.Join(claude, "p", "b.jsonl")
	write(t, a, claudeLine("s-a", "2026-01-02T03:04:05Z", "alpha one"))
	write(t, b, claudeLine("s-b", "2026-01-02T03:04:05Z", "beta cache"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	appendTo(t, a, claudeLine("s-a", "2026-01-02T04:00:00Z", "alpha three"))
	write(t, b, claudeLine("s-b", "2026-01-02T03:04:05Z", "beta queue"))
	bumpMTime(t, b)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); !strings.Contains(got, "updated 1 file (1 new message)") {
		t.Errorf("distinct sessions no longer split: %q", got)
	}
	sessionMatchesRebuild(t, dir, "claude", "s-a")
	sessionMatchesRebuild(t, dir, "claude", "s-b")
}
