package index

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Two messages with identical role, timestamp, and text within one rebuild
// pass must be deduplicated by seenMsgs, not indexed twice.
func TestRebuildExactDuplicateMessageDeduped(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two separate session files, same session id, one message that is a
	// byte-for-byte duplicate (role+timestamp+text) of the other.
	write(t, filepath.Join(claudeRoot, "p", "a.jsonl"), claudeLine("dupmsg", "2026-01-02T03:04:05Z", "exact dup needle"))
	write(t, filepath.Join(claudeRoot, "p", "b.jsonl"), claudeLine("dupmsg", "2026-01-02T03:04:05Z", "exact dup needle"))
	dir := filepath.Join(tmp, "idx")
	if err := rebuild(dir, "claude", "", currentFiles("claude"), nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "exact dup needle", All: true})
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("exact duplicate message not deduped: ss=%#v err=%v", ss, err)
	}
}

func TestEnsureDefaultDirAndInternalRecordsIntactForce(t *testing.T) {
	hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(claudeRoot, "p", "s.jsonl"), claudeLine("s1", "2026-01-02T03:04:05Z", "ensuredefault needle"))
	if err := Ensure("", "", false, nil); err != nil {
		t.Fatalf("Ensure('') err=%v", err)
	}
	dir := DefaultDir()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.RecordsSize <= 0 {
		t.Skip("no size stamp to truncate against")
	}
	if err := os.Truncate(filepath.Join(dir, "records.bin"), m.RecordsSize/2); err != nil {
		t.Fatal(err)
	}
	// Ensure() (unlike EnsureForSearch) has no outer recordsIntact gate: its
	// own internal check inside updateIndex must catch the truncated file
	// and force a rebuild.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure over truncated records.bin err=%v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "ensuredefault needle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("not healed after internal recordsIntact force: ss=%#v err=%v", ss, err)
	}
}

// EnsureForSearch's incremental-update branch (manifest fresh in version and
// scope, but files changed) must wrap a genuine updateIndex failure.
func TestEnsureForSearchWrapsUpdateIndexError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	aiderRoot := filepath.Join(tmp, "aider")
	t.Setenv("DEJA_AIDER_ROOTS", aiderRoot)
	aiderFile := filepath.Join(aiderRoot, ".aider.chat.history.md")
	write(t, aiderFile, "# aider chat started at 2026-01-01 00:00:00\n\n#### base q\n\nbase a\n")

	parent := filepath.Join(tmp, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "idx")
	o := search.Options{Query: "base"}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	// Grow the aider file (aider is never incremental-eligible, so this
	// drives updateIndex's non-append replace path) and block its own
	// staging dir by making the index's parent read-only.
	if err := os.WriteFile(aiderFile, []byte("# aider chat started at 2026-01-01 00:00:00\n\n#### base q\n\nbase a\n\n#### more\n\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(parent, 0o755) }()
	if err := EnsureForSearch(dir, o, false, nil); err == nil {
		t.Fatal("EnsureForSearch with blocked updateIndex replace path returned nil")
	}
}

// A query with zero exact-token postings must fall through to the substring
// expansion, and a real (non-NotExist) failure there must surface as a
// wrapped error; a query that does have postings but whose sessions are all
// filtered out by cutPostingsBySession must return an empty result, not an
// error.
func TestSearchSubstringPostingsDirErrorAndEmptyAfterCut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	writeTinyIndex(t, dir)

	if err := os.Chmod(filepath.Join(dir, "buckets"), 0o300); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, "buckets"), 0o755) }()
	if _, err := Search(dir, search.Options{Query: "nonexistenttoken12345"}); err == nil {
		t.Fatal("Search substring fallback over unreadable buckets dir returned nil")
	}
	_ = os.Chmod(filepath.Join(dir, "buckets"), 0o755)

	if ss, err := Search(dir, search.Options{Query: "alpha", Harness: "does-not-exist"}); err != nil || ss != nil {
		t.Fatalf("Search filtered to nothing by cutPostingsBySession = %#v err=%v", ss, err)
	}
}

// appendIncremental's own defensive branches: a nil Sessions map must be
// initialized, and a changed path absent from old.Files (violating the
// normal canAppendIncremental invariant, reachable only via this direct
// same-package call) must be dropped from Files rather than panicking.
func TestAppendIncrementalDirectDefensiveBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	if err := os.MkdirAll(filepath.Join(tmp, "idx", "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "idx", "records.bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A recognized (claude) path so parseAppendedFile actually attempts I/O
	// and fails, rather than silently no-op'ing on an unrecognized harness.
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	unknown := filepath.Join(claudeRoot, "p", "unknown.jsonl")
	if err := os.MkdirAll(filepath.Dir(unknown), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unknown, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(unknown, 0o644) }()
	old := Manifest{Files: map[string]FileState{}, Sessions: nil}
	changed := map[string]FileState{unknown: {Path: unknown, Size: 1}}
	files := map[string]FileState{unknown: {Path: unknown, Size: 1}}
	if _, _, err := appendIncremental(filepath.Join(tmp, "idx"), "", "", old, files, changed); err != nil {
		t.Fatalf("appendIncremental defensive branches err=%v", err)
	}
	if _, ok := files[unknown]; ok {
		t.Fatalf("changed path absent from old.Files should have been dropped: %#v", files)
	}
}

// A claude file that grows but becomes unreadable mid-pass must keep its old
// FileState so the next run retries it, rather than losing track of it.
func TestAppendIncrementalUnreadableGrownFileKeepsOldState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "keepold needle"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(claudeLine("s1", "2026-01-02T03:05:05Z", "keepold second")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(file, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(file, 0o644) }()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure over unreadable grown claude file err=%v", err)
	}
	_ = os.Chmod(file, 0o644)
	if ss, err := Search(dir, search.Options{Query: "keepold needle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("original content lost when append retry deferred: %#v err=%v", ss, err)
	}
}

// A second, brand-new session id appended to an already-tracked claude file
// drives appendIncremental's new-meta branch (meta.ID=="") along with its
// Started-is-zero and Title-is-empty carry-in checks.
func TestAppendIncrementalNewSessionMidFile(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "midfile first session"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// A distinct sessionId appended to the same file: the append parses a
	// brand-new session with no prior manifest entry.
	if _, err := f.WriteString(claudeLine("s2-brand-new", "2026-01-02T04:00:00Z", "midfile second session")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "midfile second session", All: true})
	if err != nil || len(ss) != 1 {
		t.Fatalf("new mid-file session not indexed: %#v err=%v", ss, err)
	}
	if ss[0].ID != "s2-brand-new" {
		t.Fatalf("new session meta not populated: %#v", ss[0])
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:s2-brand-new"]
	if !ok || meta.Title == "" || meta.Started.IsZero() {
		t.Fatalf("new session's Title/Started not carried in via metaForSession: %#v", meta)
	}
}

// appendIncremental must surface a writeBucketsConcurrent failure when a
// brand-new bucket file can't be created, and a writeManifest failure when
// the index dir itself can't accept a new temp file, distinctly.
func TestAppendIncrementalBucketAndManifestWriteErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "bucketerr existingtoken"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "buckets"), 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, "buckets"), 0o755) }()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// A brand-new token forces a brand-new bucket file, which requires
	// creating a new entry in the now read-only buckets directory.
	if _, err := f.WriteString(claudeLine("s1", "2026-01-02T03:06:05Z", "zzzneverseenbeforetoken")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Ensure(dir, "", false, nil); err == nil {
		t.Fatal("Ensure with unwritable buckets dir for a new token returned nil")
	}
	_ = os.Chmod(filepath.Join(dir, "buckets"), 0o755)

	file2 := filepath.Join(claudeRoot, "p2", "s2.jsonl")
	write(t, file2, claudeLine("s2", "2026-01-02T03:04:05Z", "manifesterr existingtoken"))
	dir2 := filepath.Join(tmp, "idx2")
	if err := Ensure(dir2, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir2, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir2, 0o755) }()
	f2, err := os.OpenFile(file2, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// Re-use the exact same token: no new bucket file is needed, so buckets
	// writes succeed even though buckets itself lives under a read-only
	// parent; only the dir-level manifest write should fail.
	if _, err := f2.WriteString(claudeLine("s2", "2026-01-02T03:06:05Z", "manifesterr existingtoken")); err != nil {
		t.Fatal(err)
	}
	f2.Close()
	if err := Ensure(dir2, "", false, nil); err == nil {
		t.Fatal("Ensure with unwritable index dir for manifest returned nil")
	}
}

// Import must surface a genuine readManifest failure distinct from the
// "no manifest yet" initEmptyIndex path.
func TestImportManifestCorruptAfterHasManifestTrue(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyBatch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(emptyBatch, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, emptyBatch); err == nil {
		t.Fatal("Import over a corrupt (but present) manifest returned nil error")
	}
}

// A corrupt bucket file colliding with an imported token's bucket must
// surface through Import -> appendImportedRecords -> loadBucket as a
// non-NotExist error, not a silent empty result.
func TestImportCorruptBucketPropagatesError(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	tok := "zzimportcollision"
	bpath := filepath.Join(dir, "buckets", bucket("t"+tok)+".bin")
	if err := os.WriteFile(bpath, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	writeSyncBatch(t, batch, []SyncRecord{
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: tok, Time: time.Now()},
	})
	if _, err := Import(dir, batch); err == nil {
		t.Fatal("Import colliding with a corrupt bucket returned nil error")
	}
}

// appendImportedRecords must fail cleanly when its records.bin path doesn't
// exist and can't be created (a missing parent directory).
func TestAppendImportedRecordsMissingDir(t *testing.T) {
	tmp := t.TempDir()
	m := &Manifest{Sessions: map[string]SessionMeta{}}
	missing := filepath.Join(tmp, "no-such-dir", "nested")
	if err := appendImportedRecords(missing, m, map[string][]Record{"k": {{Key: "k", Text: "x"}}}, map[string]SessionMeta{"k": {ID: "k"}}); err == nil {
		t.Fatal("appendImportedRecords with a missing parent dir returned nil error")
	}
}

// Importing an older-timestamped batch for a session already known from a
// newer batch must keep the newer Updated time (old.Updated.After branch).
func TestImportOlderBatchKeepsNewerUpdated(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	newer := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)
	batch1 := filepath.Join(tmp, "batch1")
	writeSyncBatch(t, batch1, []SyncRecord{{Harness: "claude", SessionID: "ord1", Project: "p", Role: "user", Text: "newer import msg", Time: newer}})
	if _, err := Import(dir, batch1); err != nil {
		t.Fatal(err)
	}
	batch2 := filepath.Join(tmp, "batch2")
	writeSyncBatch(t, batch2, []SyncRecord{{Harness: "claude", SessionID: "ord1", Project: "p", Role: "assistant", Text: "older import msg", Time: older}})
	if _, err := Import(dir, batch2); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "claude:" + ImportedSessionID("claude", "ord1")
	if !m.Sessions[key].Updated.Equal(newer) {
		t.Fatalf("Updated regressed to the older batch: %#v", m.Sessions[key])
	}
}

// Import's appendImportedRecords must surface a bucket-write failure when
// buckets/ can't accept a brand-new file.
func TestImportBlockedBucketsDirWriteError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "buckets"), 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, "buckets"), 0o755) }()
	batch := filepath.Join(tmp, "batch")
	writeSyncBatch(t, batch, []SyncRecord{{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "blockedbucket text", Time: time.Now()}})
	if _, err := Import(dir, batch); err == nil {
		t.Fatal("Import with unwritable buckets dir returned nil error")
	}
}

// exportRecords must surface a records.bin read failure distinct from the
// manifest read, a write failure into a blocked outDir, and a final
// writeManifest failure when the index dir itself is blocked.
func TestExportRecordsErrorBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)

	dir := filepath.Join(tmp, "idx")
	writeTinyIndex(t, dir)
	if err := os.Remove(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(dir, filepath.Join(tmp, "out1")); err == nil {
		t.Fatal("Export with missing records.bin returned nil error")
	}

	dir2 := filepath.Join(tmp, "idx2")
	writeTinyIndex(t, dir2)
	outDir := filepath.Join(tmp, "out2")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(outDir, 0o755) }()
	if _, err := Export(dir2, outDir); err == nil {
		t.Fatal("Export into a read-only outDir returned nil error")
	}

	dir3 := filepath.Join(tmp, "idx3")
	writeTinyIndex(t, dir3)
	if err := os.Chmod(dir3, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir3, 0o755) }()
	if _, err := Export(dir3, filepath.Join(tmp, "out3")); err == nil {
		t.Fatal("Export with a read-only index dir (final writeManifest) returned nil error")
	}
}
