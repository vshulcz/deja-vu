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
