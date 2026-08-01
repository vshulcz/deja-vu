package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Incremental ingest reuses SessionMeta for a file it has already read, so a
// title derived under older rules survives every ordinary `deja index`. Three
// title fixes shipped without a format bump and left blank titles in place
// (#784); the bump is the repair, and this pins that the repair works.
func TestStaleFormatRederivesTitles(t *testing.T) {
	root, dir := allHarnessEnv(t)
	claudeFile := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	write(t, claudeFile, claudeLine("s1", "2026-01-02T03:04:05Z", "ballast pump controller retries twice"))

	o := search.Options{Query: "ballast", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:s1"]
	if !ok {
		t.Fatalf("session not in manifest: %v", m.Sessions)
	}
	if meta.Title == "" {
		t.Fatal("no title to lose")
	}

	// What an index built by an older binary holds: a readable store whose
	// titles came from rules this build no longer uses.
	meta.Title = ""
	m.Sessions["claude:s1"] = meta
	m.Version = version - 1
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	// Touching nothing else: the transcript is unchanged, which is exactly the
	// case incremental ingest skips.
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Sessions["claude:s1"].Title; got == "" {
		t.Error("an index from an older format kept its blank title")
	}
	if _, err := os.Stat(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatal(err)
	}
}
