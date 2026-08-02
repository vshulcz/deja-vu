package index

import (
	"os"
	"path/filepath"
	"testing"
)

// A transcript whose every message strips to empty still got a manifest row:
// `last` printed a blank line for it, `show` printed a bare header, and the
// counters disagreed — brief and doctor read the manifest, stats reads the
// records (1159 against 1157 on my store) (#868).
func TestSessionsWithNothingToIndexAreNotCounted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(root, "-tmp-g")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jsonl", `{"type":"user","sessionId":"n1","cwd":"/tmp/g","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}`+"\n")
	// Nothing but whitespace: parsed as a message, never written as a record.
	write("b.jsonl", `{"type":"user","sessionId":"e2","cwd":"/tmp/g","timestamp":"2026-08-01T08:00:00Z","message":{"role":"user","content":"   "}}`+"\n")

	dir := filepath.Join(home, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportEmptySessions(); n != 1 {
		t.Errorf("empty transcripts reported = %d, want 1", n)
	}
	counts := HarnessSessionCounts(dir)
	if counts["claude"] != 1 {
		t.Errorf("manifest holds %d claude sessions, want 1", counts["claude"])
	}
	ss, err := Recent(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "n1" {
		t.Fatalf("recent = %+v, want only n1", ss)
	}
	// The session that does have content keeps its row across a rebuild.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if counts := HarnessSessionCounts(dir); counts["claude"] != 1 {
		t.Errorf("after rebuild manifest holds %d claude sessions, want 1", counts["claude"])
	}
}

// OlderFormat must separate an index this build cannot read from one it cannot
// read at all: doctor says "written by an older deja" on the first, and a
// corrupt manifest is not that (#877).
func TestOlderFormatOnlyReportsAReadableOldIndex(t *testing.T) {
	dir := t.TempDir()
	if err := writeManifest(dir, Manifest{Version: version, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if OlderFormat(dir) {
		t.Error("a current index was called old")
	}
	if err := writeManifest(dir, Manifest{Version: version - 1, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if !OlderFormat(dir) {
		t.Error("an index from an older format was not reported")
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("not a gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if OlderFormat(dir) {
		t.Error("an unreadable manifest was called an older format")
	}
}
