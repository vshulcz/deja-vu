package index

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Losing the whole postings directory was caught in #946. Losing one file out
// of it was not: every token that lived there answered "no matches in N indexed
// sessions" while its text sat in the record log, `deja index` said the index
// was up to date and `doctor` called it healthy (#1088). A partial copy or an
// interrupted sync leaves exactly this — some files, not none.
func TestOneMissingBucketFileCountsAsDamage(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")

	// Two words far enough apart to land in different buckets, so removing one
	// file leaves the other's postings in place.
	writeLines(t, filepath.Join(claude, "project", "s.jsonl"),
		claudeLine("s1", "2026-01-01T00:01:00Z", "pool exhausted"),
		claudeLine("s1", "2026-01-01T00:02:00Z", "vacuum deadlock"))
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("a freshly built index was reported as damaged")
	}

	buckets := filepath.Join(dir, "buckets")
	before := countBucketFiles(buckets)
	if before < 2 {
		t.Fatalf("this case needs more than one bucket file, got %d", before)
	}
	entries, err := os.ReadDir(buckets)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(buckets, entries[0].Name())); err != nil {
		t.Fatal(err)
	}
	// The premise: the directory is still there and still holds files, which is
	// everything the older check looked at.
	if !hasBucketFile(buckets) {
		t.Fatal("the probe emptied the directory; it is meant to leave files behind")
	}
	if !Damaged(dir) {
		t.Errorf("an index missing %d of %d bucket files was reported as intact", 1, before)
	}
}

// A manifest written before the count existed decodes with it zero, and must
// not send every store built by an older deja into a rebuild loop.
func TestAnUncountedManifestIsNotDamaged(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	writeLines(t, filepath.Join(claude, "project", "s.jsonl"),
		claudeLine("s1", "2026-01-01T00:01:00Z", "pool exhausted"))
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.BucketFiles = 0
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	// writeManifestOnly restamps the count, so drop it the way an old build
	// left it: no stamp on disk at all.
	raw, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw.BucketFiles = 0
	if !recordsIntact(dir, raw) {
		t.Error("an index whose manifest carries no bucket count was called damaged")
	}
}
