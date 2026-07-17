package index

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A *os.File closed out from under a recordWriter (or handed to it already
// closed) makes every write path fail its underlying I/O: this exercises
// newRecordWriter's Seek error, write()'s two Write error returns, Close()'s
// Flush and file-Close error returns, writeRecord's Seek/Write errors, and
// readRecordAt's Seek error -- all otherwise unreachable since a healthy
// *os.File's small buffered writes never fail in a hermetic test.
func TestRecordIOErrorsViaClosedFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "rec.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := newRecordWriter(f); err == nil {
		t.Fatal("newRecordWriter on closed file returned nil error")
	}

	// Build a recordWriter directly (same package) with a 1-byte buffer so
	// even the 4-byte header write forces an immediate flush to the closed
	// underlying file, hitting write()'s first Write error return.
	f2, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{Key: "k", SourcePath: "s", Role: "user", Text: "hello world this is long enough to overflow", Time: time.Now()}
	if err := f2.Close(); err != nil {
		t.Fatal(err)
	}
	rw := &recordWriter{f: f2, w: bufio.NewWriterSize(f2, 1)}
	if _, err := rw.write(rec); err == nil {
		t.Fatal("write on closed file returned nil error")
	}
	if err := rw.Close(); err == nil {
		t.Fatal("Close on already-closed file returned nil error")
	}

	// A bigger buffer means the small header write is only buffered, not
	// flushed, so the body write is what actually fails.
	f2b, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f2b.Close(); err != nil {
		t.Fatal(err)
	}
	rwBig := &recordWriter{f: f2b, w: bufio.NewWriterSize(f2b, 8)}
	if _, err := rwBig.write(rec); err == nil {
		t.Fatal("write (body) on closed file returned nil error")
	}

	// writeRecord's Seek succeeds on a read-only fd; only the subsequent
	// Write fails, since the fd was never opened for writing.
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f3, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f3.Close()
	if _, err := writeRecord(f3, rec); err == nil {
		t.Fatal("writeRecord on a read-only file returned nil error")
	}

	f4, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f4.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecordAt(f4, 0); err == nil {
		t.Fatal("readRecordAt on closed file returned nil error")
	}
}

// writeGobAtomic must clean up its temp file and surface the error when the
// value cannot be gob-encoded (e.g. a func value).
func TestWriteGobAtomicEncodeError(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bad.gob")
	if err := writeGobAtomic(p, func() {}); err == nil {
		t.Fatal("writeGobAtomic with unencodable value returned nil error")
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("writeGobAtomic left temp file behind: err=%v", err)
	}
}

func TestEncodeDecodePostingsEdgeCases(t *testing.T) {
	if got := encodePostings(nil); got != nil {
		t.Fatalf("encodePostings(nil)=%v want nil", got)
	}
	// First varint (offset delta) decodes fully; second varint (sid) is a
	// truncated continuation byte with nothing following.
	got := decodePostings([]byte{1, 0x80})
	if len(got) != 0 {
		t.Fatalf("decodePostings truncated sid = %#v, want empty", got)
	}
}

func TestRecordsIntactMissingFile(t *testing.T) {
	tmp := t.TempDir()
	if !recordsIntact(tmp, Manifest{RecordsSize: 0}) {
		t.Fatal("recordsIntact false for RecordsSize<=0")
	}
	if recordsIntact(tmp, Manifest{RecordsSize: 10}) {
		t.Fatal("recordsIntact true when records.bin is missing")
	}
}

// intersectSubstringPostings must skip non-.bin entries and subdirectories in
// buckets/, skip a bucket file it can't open, skip a posting whose declared
// offset/length falls past the file's actual data, and surface a genuine
// ReadDir failure (as opposed to ErrNotExist, handled elsewhere).
func TestIntersectSubstringPostingsSkipBranches(t *testing.T) {
	tmp := t.TempDir()
	bucketsDir := filepath.Join(tmp, "buckets")
	if err := os.MkdirAll(filepath.Join(bucketsDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucketsDir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucketsDir, "corrupt.bin"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	goodPath := filepath.Join(bucketsDir, "good.bin")
	if err := writeBucket(goodPath, map[string][]posting{"talpha": {{Off: 1, Sid: 1}}}); err != nil {
		t.Fatal(err)
	}
	// Truncate the good bucket right after its directory section so any
	// ReadAt into the postings payload runs past EOF.
	fi, err := os.Stat(goodPath)
	if err != nil {
		t.Fatal(err)
	}
	entries, gf, err := openBucketDir(goodPath)
	if err != nil {
		t.Fatal(err)
	}
	dataStart := entries[0].off
	gf.Close()
	if err := os.Truncate(goodPath, int64(dataStart)); err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= int64(dataStart) {
		t.Fatal("test setup: nothing to truncate")
	}

	got, err := intersectSubstringPostings(tmp, []string{"alp"})
	if err != nil {
		t.Fatalf("intersectSubstringPostings with skip branches err=%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("truncated postings payload should yield no matches, got %#v", got)
	}

	if runtime.GOOS != "windows" {
		unreadableParent := filepath.Join(tmp, "unreadable-parent")
		unreadableBuckets := filepath.Join(unreadableParent, "buckets")
		if err := os.MkdirAll(unreadableBuckets, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(unreadableBuckets, 0o300); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(unreadableBuckets, 0o755)
		if _, err := intersectSubstringPostings(unreadableParent, []string{"x"}); err == nil {
			t.Fatal("intersectSubstringPostings on unreadable buckets dir returned nil")
		}
	}
}

// lockDir must surface an OpenFile failure when the lock file's parent
// directory exists but cannot accept a new file.
func TestLockDirOpenFileFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "idx")
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0o755)
	if _, err := lockDir(dir); err == nil {
		t.Fatal("lockDir with read-only parent returned nil error")
	}
}

// exportRecords must resolve dir=="", fall back to the record's Key when
// SourcePath is empty, and skip a record whose Key has no manifest entry
// rather than exporting a session-less orphan.
func TestExportDefaultDirSourceFallbackAndOrphanSkip(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := DefaultDir()
	writeTinyIndex(t, dir)
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	// Append two records directly (same-package, bypassing session ingest):
	// one with a real Key but empty SourcePath (must fall back to r.Key as
	// the export source grouping), one with a Key absent from the manifest
	// (must be skipped as an orphan).
	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "claude:s1", SourcePath: "", Role: "user", Text: "no source path text", Time: base}); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "ghost:nope", SourcePath: "orphan-source", Role: "user", Text: "orphan text", Time: base}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}

	n, err := Export("", filepath.Join(tmp, "batch-out"))
	if err != nil {
		t.Fatalf("Export('') err=%v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(tmp, "batch-out", "*.jsonl"))
	var all string
	for _, p := range matches {
		b, _ := os.ReadFile(p)
		all += string(b)
	}
	if strings.Contains(all, "orphan text") {
		t.Fatal("orphan record with no manifest session was exported")
	}
	if !strings.Contains(all, "no source path text") {
		t.Fatal("record with empty SourcePath (key fallback) was not exported")
	}
	if n == 0 {
		t.Fatal("Export('') exported nothing from the tiny fixture index")
	}
}

// Import must resolve dir=="", fail cleanly when the destination path is a
// regular file (initEmptyIndex's MkdirAll fails), and surface a bad glob
// pattern instead of silently returning zero.
func TestImportDefaultDirAndInitAndGlobErrors(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	emptyBatch := filepath.Join(tmp, "empty-batch")
	if err := os.MkdirAll(emptyBatch, 0o755); err != nil {
		t.Fatal(err)
	}
	if n, err := Import("", emptyBatch); err != nil || n != 0 {
		t.Fatalf("Import('') n=%d err=%v", n, err)
	}

	blockedDir := filepath.Join(tmp, "blocked-index")
	if err := os.WriteFile(blockedDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(blockedDir, emptyBatch); err == nil {
		t.Fatal("Import into a file path returned nil error")
	}

	badPattern := filepath.Join(tmp, "batch-with-[unclosed")
	if _, err := Import(filepath.Join(tmp, "another-index"), badPattern); err == nil {
		t.Fatal("Import with malformed glob pattern returned nil error")
	}
}

// initEmptyIndex must surface an os.Create failure when records.bin's path
// is blocked by a pre-existing directory.
func TestInitEmptyIndexRecordsCreateFailure(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "idx")
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "records.bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := initEmptyIndex(dir); err == nil {
		t.Fatal("initEmptyIndex with records.bin as a directory returned nil error")
	}
}

// readSyncFile must surface a bufio.Scanner error other than io.EOF: a
// single line larger than the scanner's 8MiB max token size trips
// bufio.ErrTooLong.
func TestReadSyncFileScannerTooLong(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "huge.jsonl")
	huge := make([]byte, 9*1024*1024)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := os.WriteFile(p, huge, 0o644); err != nil {
		t.Fatal(err)
	}
	err := readSyncFile(p, func(SyncRecord) error { return nil })
	if err == nil {
		t.Fatal("readSyncFile with an oversized line returned nil error")
	}
}
