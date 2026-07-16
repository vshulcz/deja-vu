package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func claudeLine(sid, ts, text string) string {
	return `{"type":"user","sessionId":"` + sid + `","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

// A records.bin cut short by a crash must not read as a fresh index that
// silently returns fewer messages: the size stamp catches the short file and
// the next run rebuilds.
func TestSearchRecoversFromTruncatedRecords(t *testing.T) {
	root, dir := allHarnessEnv(t)
	claudeFile := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	write(t, claudeFile, claudeLine("s1", "2026-01-02T03:04:05Z", "recneedle one")+claudeLine("s1", "2026-01-02T03:04:06Z", "recneedle two"))

	o := search.Options{Query: "recneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, o); err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("baseline search wrong: %#v err=%v", ss, err)
	}

	// Truncate records.bin to simulate a torn write mid-record.
	recs := filepath.Join(dir, "records.bin")
	fi, err := os.Stat(recs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(recs, fi.Size()/2); err != nil {
		t.Fatal(err)
	}

	// Direct Search must flag corruption rather than return a half-scan.
	if _, err := Search(dir, o); !IsCorrupt(err) {
		t.Fatalf("Search over truncated records.bin err=%v, want corrupt index", err)
	}
	// The source files are unchanged, so the only way to heal is the size stamp
	// forcing a rebuild.
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, o)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("not healed after rebuild: %#v err=%v", ss, err)
	}
}

// A manifest.gob left half-written by a crash must fail to decode and trigger a
// rebuild, never load as an empty-but-valid index.
func TestEnsureRecoversFromTornManifest(t *testing.T) {
	root, dir := allHarnessEnv(t)
	claudeFile := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	write(t, claudeFile, claudeLine("s1", "2026-01-02T03:04:05Z", "manneedle here"))

	o := search.Options{Query: "manneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}

	man := filepath.Join(dir, "manifest.gob")
	fi, err := os.Stat(man)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(man, fi.Size()/2); err != nil {
		t.Fatal(err)
	}

	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatalf("ensure did not heal torn manifest: %v", err)
	}
	if ss, err := Search(dir, o); err != nil || len(ss) != 1 {
		t.Fatalf("not healed: %#v err=%v", ss, err)
	}
}

// The incremental append path writes sessions.gob before manifest.gob so a
// crash between them leaves the *old* manifest in place. This reconstructs that
// exact crash state (old manifest, new sessions + records) and asserts the new
// message is reindexed and found rather than silently dropped.
func TestAppendCrashBetweenSessionsAndManifestReindexes(t *testing.T) {
	root, dir := allHarnessEnv(t)
	claudeFile := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	write(t, claudeFile, claudeLine("s1", "2026-01-02T03:04:05Z", "appendneedle first"))

	o := search.Options{Query: "appendneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	oldManifest, err := os.ReadFile(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		t.Fatal(err)
	}

	// Append a second complete line and let the incremental path commit it.
	f, err := os.OpenFile(claudeFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(claudeLine("s1", "2026-01-02T03:04:07Z", "appendneedle second")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}

	// Simulate the crash: manifest.gob reverts to its pre-append contents while
	// sessions.gob and records.bin already hold the new message.
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), oldManifest, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "appendneedle second", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Fatalf("appended message silently lost after simulated crash: %#v", ss)
	}
}

// writeManifest must commit atomically: no leftover temp files, and the size
// stamp must match records.bin exactly so recordsIntact never false-positives.
func TestWriteManifestAtomicAndStamped(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p", "s.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "stampneedle"))
	if err := EnsureForSearch(dir, search.Options{Query: "stampneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if m.RecordsSize != fi.Size() {
		t.Fatalf("RecordsSize=%d, records.bin=%d", m.RecordsSize, fi.Size())
	}
	if !recordsIntact(dir, m) {
		t.Fatal("recordsIntact false on a clean index")
	}
}
