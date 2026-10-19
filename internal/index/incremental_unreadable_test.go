package index

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A chmod changes no size or mtime and a directory that cannot be listed adds
// no file, so the incremental pass called the index up to date while two paths
// were unreadable; only a rebuild said so (#5393).
func TestIncrementalPassReportsPathsItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	root, dir := allHarnessEnv(t)
	locked := filepath.Join(root, "claude", "-tmp-p1", "a.jsonl")
	write(t, locked, claudeLine("a1", "2026-01-02T03:04:05Z", "lockneedle one"))
	write(t, filepath.Join(root, "claude", "-tmp-p1", "b.jsonl"),
		claudeLine("b1", "2026-01-02T04:04:05Z", "lockneedle two"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(root, "claude", "-tmp-p2")
	write(t, filepath.Join(newDir, "c.jsonl"), claudeLine("c1", "2026-01-03T03:04:05Z", "lockneedle three"))
	for _, p := range []string{locked, newDir} {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(newDir, 0o755); _ = os.Chmod(locked, 0o644) })

	if fresh, _ := UpToDate(dir, ""); fresh {
		t.Fatal("the index is up to date with two paths it cannot read")
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 2 {
		t.Fatalf("an incremental pass counted %d unreadable paths, want 2: %+v", got, IngestHealth(dir))
	}
	if fresh, n := UpToDate(dir, ""); !fresh || n != 2 {
		t.Fatalf("once recorded the index is current: fresh=%v sessions=%d", fresh, n)
	}
	// The sessions already read from the locked file stay searchable.
	if !hasHit5393(t, dir, "lockneedle one") {
		t.Error("the locked transcript's indexed session left recall")
	}

	if err := os.Chmod(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o644); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := UpToDate(dir, ""); fresh {
		t.Fatal("readable again, and the index still holds the old errors")
	}
	if err := EnsureForSearch(dir, search.Options{Query: "lockneedle", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 0 {
		t.Fatalf("readable again, still %d unreadable: %+v", got, IngestFilesReport(dir))
	}
	if fresh, n := UpToDate(dir, ""); !fresh || n != 3 {
		t.Fatalf("after the paths open again: fresh=%v sessions=%d, want 3", fresh, n)
	}
}

func hasHit5393(t *testing.T, dir, q string) bool {
	t.Helper()
	res, err := Search(dir, search.Options{Query: q, All: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(res) > 0
}

// A directory locked over transcripts already indexed: the pass keeps their
// sessions, names them as unreadable, and is then up to date, instead of
// running again on every search (#5393).
func TestALockedDirectoryOverIndexedTranscriptsSettles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root, dir := allHarnessEnv(t)
	proj := filepath.Join(root, "claude", "-tmp-p1")
	write(t, filepath.Join(proj, "a.jsonl"), claudeLine("a1", "2026-01-02T03:04:05Z", "settleneedle"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(proj, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(proj, 0o755) })
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got == 0 {
		t.Fatalf("the locked directory went unnamed: %+v", IngestFilesReport(dir))
	}
	if fresh, n := UpToDate(dir, ""); !fresh || n != 1 {
		t.Fatalf("after the pass that named it: fresh=%v sessions=%d", fresh, n)
	}
	if stale, err := EnsureForSearchStale(dir, search.Options{Query: "settleneedle", All: true}, nil); err != nil || stale {
		t.Fatalf("a search hands the same pass to a warmup every time: stale=%v err=%v", stale, err)
	}
	if !hasHit5393(t, dir, "settleneedle") {
		t.Error("the sessions behind the locked directory left recall")
	}
}

// A parse error on a file that has not changed is still the file's. Only the
// walk's own refusals clear when a later walk reads the path (#5393).
func TestAParseErrorOnAnUnchangedFileStands(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p1", "a.jsonl"), claudeLine("a1", "2026-01-02T03:04:05Z", "standneedle"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "claude", "-tmp-p1", "a.jsonl")
	m.IngestFiles = map[string]FileIngest{p: {Error: "decode: unexpected end of JSON input"}}
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := IngestFilesReport(dir)[p].Error; got == "" {
		t.Fatal("a parse error on an unchanged file was cleared by a walk that only statted it")
	}
	if fresh, _ := UpToDate(dir, ""); !fresh {
		t.Fatal("a parse error alone keeps the index from being up to date")
	}
}

// A locked transcript deleted afterwards has nothing left to report.
func TestALockedTranscriptDeletedStopsBeingReported(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	root, dir := allHarnessEnv(t)
	p := filepath.Join(root, "claude", "-tmp-p1", "a.jsonl")
	write(t, p, claudeLine("a1", "2026-01-02T03:04:05Z", "goneneedle"))
	write(t, filepath.Join(root, "claude", "-tmp-p1", "b.jsonl"), claudeLine("b1", "2026-01-02T03:04:05Z", "goneneedle too"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 1 {
		t.Fatalf("locked: %d unreadable, want 1", got)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 0 {
		t.Fatalf("deleted, and still %d unreadable: %+v", got, IngestFilesReport(dir))
	}
	if fresh, _ := UpToDate(dir, ""); !fresh {
		t.Fatal("not up to date after the deletion was recorded")
	}
}

// A file that failed to parse and was then deleted has nothing left to
// report; kept, it failed every later `deja index` with exit status 3 (#5396).
func TestAParseErrorOnADeletedFileClears(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p1", "a.jsonl"), claudeLine("a1", "2026-01-02T03:04:05Z", "clearneedle"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	gonePath := filepath.Join(root, "claude", "-tmp-p1", "b.jsonl")
	m.IngestFiles = map[string]FileIngest{gonePath: {Error: "decode: unexpected end of JSON input"}}
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 0 {
		t.Fatalf("a deleted file is still reported unreadable: %+v", IngestFilesReport(dir))
	}
}
