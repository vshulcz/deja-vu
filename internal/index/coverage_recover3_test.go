package index

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestLoadDirectCall(t *testing.T) {
	hermeticIndexEnv(t)
	if got := load(""); got != nil {
		t.Fatalf("load('') on an empty hermetic env = %#v, want nil/empty", got)
	}
}

func TestWriteGobCreateError(t *testing.T) {
	tmp := t.TempDir()
	if err := writeGob(filepath.Join(tmp, "missing-parent", "x.gob"), map[string]int{"a": 1}); err == nil {
		t.Fatal("writeGob with missing parent dir returned nil error")
	}
}

// lastCompleteLineOffset's ReadAt must fail cleanly (return size) when asked
// to read more than the file actually holds.
func TestLastCompleteLineOffsetReadAtError(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "short.txt")
	if err := os.WriteFile(p, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lastCompleteLineOffset(p, 1<<20); got != 1<<20 {
		t.Fatalf("lastCompleteLineOffset with oversized size = %d, want %d", got, 1<<20)
	}
}

func TestDecodeRecordMissingRole(t *testing.T) {
	buf := appendField(nil, "key")
	buf = appendField(buf, "src")
	if _, err := decodeRecord(buf); err == nil {
		t.Fatal("decodeRecord missing role field returned nil error")
	}
}

// eachRecord's readRecord can surface a genuine I/O error distinct from a
// clean EOF/truncation: opening a directory as if it were records.bin means
// the very first Read returns "is a directory", not io.EOF.
func TestEachRecordNonEOFReadError(t *testing.T) {
	tmp := t.TempDir()
	dirAsFile := filepath.Join(tmp, "not-a-file")
	if err := os.MkdirAll(dirAsFile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := eachRecord(dirAsFile, func(Record) {}); err == nil {
		t.Fatal("eachRecord over a directory returned nil error")
	}
}

func TestIntersectPostingsEmptyKeys(t *testing.T) {
	if got, err := intersectPostings(t.TempDir(), nil); err != nil || got != nil {
		t.Fatalf("intersectPostings(nil keys)=%#v err=%v", got, err)
	}
}

// readBucket and readBucketToken must surface a ReadAt failure when a
// bucket's declared postings payload runs past the truncated file's end.
func TestReadBucketAndTokenReadAtError(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "b.bin")
	if err := writeBucket(p, map[string][]posting{"talpha": {{Off: 1, Sid: 1}}}); err != nil {
		t.Fatal(err)
	}
	entries, f, err := openBucketDir(p)
	if err != nil {
		t.Fatal(err)
	}
	dataStart := entries[0].off
	f.Close()
	if err := os.Truncate(p, int64(dataStart)); err != nil {
		t.Fatal(err)
	}
	if _, err := readBucket(p); err == nil {
		t.Fatal("readBucket with truncated payload returned nil error")
	}
	if _, err := readBucketToken(p, "talpha"); err == nil {
		t.Fatal("readBucketToken with truncated payload returned nil error")
	}
}

// importedSessions must skip a sync-import-tagged record whose session key
// has no manifest entry (a manifest edited out from under it).
func TestImportedSessionsOrphanRecordSkipped(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	rf, err := os.Create(filepath.Join(tmp, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "claude:ghost", SourcePath: syncImportPath, Role: "user", Text: "orphan", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	m := Manifest{Sessions: map[string]SessionMeta{}}
	if err := writeManifest(tmp, m); err != nil {
		t.Fatal(err)
	}
	got := importedSessions(tmp)
	if len(got.sessions) != 0 {
		t.Fatalf("importedSessions kept an orphan record: %#v", got.sessions)
	}
}

// parseChangedFile/parseAppendedFile must fall back to a full opencode parse
// (not the *Since variant) when the old FileState carries no LastUpdated.
func TestParseOpencodeFallbackWithoutLastUpdated(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	db := os.Getenv("DEJA_OPENCODE_DB")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = tmp
	if _, err := parseChangedFile("", db, FileState{}); err != nil {
		t.Fatalf("parseChangedFile opencode fallback err=%v", err)
	}
	if _, err := parseAppendedFile("", db, FileState{}); err != nil {
		t.Fatalf("parseAppendedFile opencode fallback err=%v", err)
	}
	if _, err := parseAppendedFile("", db, FileState{LastUpdated: time.Now().UnixNano()}); err != nil {
		t.Fatalf("parseAppendedFile opencode since-branch err=%v", err)
	}
}

func TestScanRecordsMetaMissingAndOpenError(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	rf, err := os.Create(filepath.Join(tmp, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "nope:1", SourcePath: "s", Role: "user", Text: "x", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := scanRecords(tmp, Manifest{Sessions: map[string]SessionMeta{}}, search.Options{}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("scanRecords with unknown session key=%#v err=%v", got, err)
	}
	if _, err := scanRecords(t.TempDir(), Manifest{}, search.Options{}, []int64{0}); err == nil {
		t.Fatal("scanRecords with offsets over a missing records.bin returned nil")
	}
}

func TestCutPostingsBySessionTieBreak(t *testing.T) {
	updated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := Manifest{Sessions: map[string]SessionMeta{
		"h:1": {ID: "1", Harness: "h", Ord: 1, Updated: updated},
		"h:2": {ID: "2", Harness: "h", Ord: 2, Updated: updated},
	}}
	posts := []posting{{Off: 1, Sid: 1}, {Off: 2, Sid: 2}}
	got := cutPostingsBySession(posts, m, search.Options{})
	if len(got) != 2 {
		t.Fatalf("cutPostingsBySession tie-break=%#v", got)
	}
}

// The full-replace path of updateIndex (a harness never eligible for
// incremental append, e.g. aider) must skip a file whose parse fails,
// keeping its FileState for an existing entry and dropping it entirely for
// a brand-new one, and must surface a blocked staging directory.
func TestUpdateIndexReplacePathParseFailureSkipBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	aiderRoot := filepath.Join(tmp, "aider")
	t.Setenv("DEJA_AIDER_ROOTS", aiderRoot)
	aiderFile := filepath.Join(aiderRoot, ".aider.chat.history.md")
	write(t, aiderFile, "# aider chat started at 2026-01-01 00:00:00\n\n#### aiderbase question\n\naiderbase answer\n")

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Grow the file (so it's picked up as "changed") but make it unreadable;
	// aider is not incremental-eligible, so this drives the replace path's
	// parseChangedFile-error branch for a file already known to old.Files.
	if err := os.WriteFile(aiderFile, []byte(strReplaceGrow()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(aiderFile, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure with unreadable known aider file err=%v", err)
	}
	_ = os.Chmod(aiderFile, 0o644)
	if ss, err := Search(dir, search.Options{Query: "aiderbase", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("original aider content must survive a skipped re-parse: ss=%#v err=%v", ss, err)
	}

	// A brand-new file that's unreadable from the moment currentFiles()
	// notices it: old.Files has no entry for it, so the skip branch takes
	// the delete(files, p) path instead of restoring an old FileState.
	newAiderFile := filepath.Join(aiderRoot, "second", ".aider.chat.history.md")
	write(t, newAiderFile, "# aider chat started at 2026-01-01 00:00:00\n\n#### unreadable q\n\na\n")
	if err := os.Chmod(newAiderFile, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(newAiderFile, 0o644)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure with new unreadable aider file err=%v", err)
	}

	// Blocked own staging dir: canAppendIncremental is false for aider, so a
	// content-changed aider file drives the replace path's MkdirAll.
	blockedParent := filepath.Join(tmp, "blocked-parent")
	if err := os.MkdirAll(blockedParent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir2 := filepath.Join(blockedParent, "idx2")
	if err := Ensure(dir2, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aiderFile, []byte("# aider chat started at 2026-01-01 00:00:00\n\n#### grownmore q\n\na\nmore content to grow the file size further\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blockedParent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blockedParent, 0o755)
	if err := Ensure(dir2, "", false, nil); err == nil {
		t.Fatal("Ensure replace-path with blocked own staging dir returned nil")
	}
}

func strReplaceGrow() string {
	return "# aider chat started at 2026-01-01 00:00:00\n\n#### aiderbase question\n\naiderbase answer\n\n#### more\n\nmore\n"
}

// updateIndex's replace path must silently drop a raw record with an empty
// SourcePath and one whose Key has no manifest entry, rather than exporting
// or resurrecting a ghost session.
func TestUpdateIndexReplacePathOrphanRecordsDropped(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	aiderRoot := filepath.Join(tmp, "aider")
	t.Setenv("DEJA_AIDER_ROOTS", aiderRoot)
	aiderFile := filepath.Join(aiderRoot, ".aider.chat.history.md")
	write(t, aiderFile, "# aider chat started at 2026-01-01 00:00:00\n\n#### keep question\n\nkeep answer\n")

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "claude:has-empty-source", SourcePath: "", Role: "user", Text: "empty source text", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "ghost:nowhere", SourcePath: "some-untracked-source", Role: "user", Text: "ghost text", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}

	// A second aider file addition (aider is never incremental-eligible)
	// forces the replace path, which rescans dir/records.bin including our
	// two injected orphan records.
	write(t, filepath.Join(aiderRoot, "second", ".aider.chat.history.md"),
		"# aider chat started at 2026-01-01 01:00:00\n\n#### keep2 question\n\nkeep2 answer\n")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, search.Options{Query: "empty source text", All: true}); err != nil || len(ss) != 0 {
		t.Fatalf("record with empty SourcePath and no session was resurrected: %#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "ghost text", All: true}); err != nil || len(ss) != 0 {
		t.Fatalf("orphan ghost record was resurrected: %#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "keep answer", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("legitimate record lost during replace path: %#v err=%v", ss, err)
	}
}
