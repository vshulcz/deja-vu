package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A crash after records flushed but before the manifest committed leaves an
// orphan tail; re-appending it used to duplicate messages silently (#181).
func TestRecordsIntactRejectsOrphanTail(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{RecordsSize: 10}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	if !recordsIntact(dir, m) {
		t.Fatal("exact size must be intact")
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 14), 0o600); err != nil {
		t.Fatal(err)
	}
	if recordsIntact(dir, m) {
		t.Fatal("orphan tail must not count as intact")
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 6), 0o600); err != nil {
		t.Fatal(err)
	}
	if recordsIntact(dir, m) {
		t.Fatal("truncated log must not count as intact")
	}
}

func TestSwapIndexDirAndRecovery(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "index.db")
	tmp := filepath.Join(base, "tmp-build")
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "marker"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "marker"))
	if string(b) != "new" {
		t.Fatalf("marker = %q", b)
	}
	if _, err := os.Stat(dir + ".old"); !os.IsNotExist(err) {
		t.Fatal(".old must be cleaned up after a completed swap")
	}
	// Interrupted swap: dir gone, .old parked — recovery restores it.
	if err := os.Rename(dir, dir+".old"); err != nil {
		t.Fatal(err)
	}
	recoverIndexDir(dir)
	b, _ = os.ReadFile(filepath.Join(dir, "marker"))
	if string(b) != "new" {
		t.Fatalf("recovered marker = %q", b)
	}
	// With a healthy dir, recovery only clears leftovers.
	if err := os.MkdirAll(dir+".old", 0o700); err != nil {
		t.Fatal(err)
	}
	recoverIndexDir(dir)
	if _, err := os.Stat(dir + ".old"); !os.IsNotExist(err) {
		t.Fatal("stale .old must be removed when dir is healthy")
	}
}

func TestWriteBucketLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ab.bin")
	if err := writeBucket(p, map[string][]posting{"tok": {{Off: 1, Sid: 2}}}); err != nil {
		t.Fatal(err)
	}
	got, err := readBucket(p)
	if err != nil || len(got["tok"]) != 1 {
		t.Fatalf("bucket roundtrip = %#v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWriteTombstonesAtomicNoTemp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := writeTombstones(map[string]bool{"claude:s1": true, "codex:s2": true}); err != nil {
		t.Fatal(err)
	}
	got := readTombstones()
	if !got["claude:s1"] || !got["codex:s2"] {
		t.Fatalf("tombstones = %#v", got)
	}
	if _, err := os.Stat(tombstonePath() + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
	// Overwrite shrinks the set — rename must fully replace, not append.
	if err := writeTombstones(map[string]bool{"claude:s1": true}); err != nil {
		t.Fatal(err)
	}
	if got := readTombstones(); got["codex:s2"] || !got["claude:s1"] {
		t.Fatalf("after shrink = %#v", got)
	}
}

func TestIngestHealthPersistedToManifest(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	dir := filepath.Join(tmp, "index.db")
	proj := filepath.Join(claudeRoot, "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"good line"}}` + "\n" +
		`{"broken json` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	h := IngestHealth(dir)
	if h["claude"].MalformedLines != 1 {
		t.Fatalf("ingest health = %#v, want claude malformed=1", h)
	}
}

func TestExportDeferredCommitsWatermarkOnlyOnAck(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"watermark ack test"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "out")
	n, commit, err := ExportDeferred(dir, out)
	if err != nil || n != 1 {
		t.Fatalf("deferred export = %d, %v", n, err)
	}
	// Not committed: a re-export must still see the record.
	n2, commit2, err := ExportDeferred(dir, filepath.Join(tmp, "out2"))
	if err != nil || n2 != 1 {
		t.Fatalf("pre-ack re-export = %d, %v — watermark advanced before ack", n2, err)
	}
	_ = commit2
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	// Committed: nothing new.
	n3, _, err := ExportDeferred(dir, filepath.Join(tmp, "out3"))
	if err != nil || n3 != 0 {
		t.Fatalf("post-ack export = %d, %v — watermark not persisted", n3, err)
	}
	// Sessions must survive the core-only manifest write.
	ss, err := Recent(dir, 5)
	if err != nil || len(ss) == 0 {
		t.Fatalf("sessions clobbered by watermark commit: %v, %v", ss, err)
	}
}

// A file indexed while its first line was still torn records SafeSize==0 with
// bytes on disk. The next pass must NOT resume an append from that ambiguous 0
// (which drops the first message or duplicates a lone line) — it must fall back
// to a full re-index (#appendloss).
func TestAppendSkipsIncrementalWhenNothingWasComplete(t *testing.T) {
	claude := filepath.Join(t.TempDir(), "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	p := filepath.Join(claude, "-proj", "s1.jsonl") // harnessForPath -> "claude"
	changed := map[string]FileState{p: {Path: p, Size: 120}}
	old := map[string]FileState{p: {Path: p, Size: 40, SafeSize: 0}}
	if canAppendIncremental(changed, old) {
		t.Fatal("SafeSize==0 with bytes on disk must force full re-index, not append")
	}
	// A normal grown file with a real SafeSize still appends incrementally.
	old[p] = FileState{Path: p, Size: 40, SafeSize: 40}
	if !canAppendIncremental(changed, old) {
		t.Fatal("a file with a complete prior line should still append incrementally")
	}
}

func TestProjectRelevantRanksByIDFNotFiller(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// s1: rare topical term "quetzalcoatl". s2: only common filler "need the".
	mk := func(id, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("s1", "the quetzalcoatl migration and need the fix")
	mk("s2", "need the report and need the update")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A prompt full of filler plus the one rare term must rank s1 first.
	got, _, err := ProjectRelevant(dir, []string{"app"}, []string{"need", "the", "quetzalcoatl"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "s1" {
		t.Fatalf("ranking = %v, want s1 first (rare term dominates filler)", got)
	}
}
