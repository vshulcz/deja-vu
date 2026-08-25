package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The batch file's name comes from the sending machine — scp copies whatever
// matched over there — and the import built its errors out of it raw, so a
// name with an escape byte in it rewrote the screen of whoever was watching a
// sync fail (#1847).
func TestAnImportErrorDoesNotHandTheTerminalAPeersFileName(t *testing.T) {
	dir := t.TempDir()
	batch := filepath.Join(dir, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "deja-sync-\x1b[31mHACK\rrewound-" + strings.Repeat("w", 120) + ".jsonl"
	if err := os.WriteFile(filepath.Join(batch, name), []byte("{not a record}\n"), 0o644); err != nil {
		t.Skipf("the filesystem refused the name: %v", err)
	}

	_, err := Import(filepath.Join(dir, "index.db"), batch)
	if err == nil {
		t.Fatal("a batch of nonsense imported cleanly, so there is no error to inspect")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\x1b\r") {
		t.Errorf("the error carries an escape or a rewind: %q", msg[:min(len(msg), 120)])
	}
	// The control: the reader can still tell which file failed.
	if !strings.Contains(msg, "deja-sync-") || !strings.Contains(msg, ".jsonl") {
		t.Errorf("the error no longer names the file at all: %q", msg[:min(len(msg), 120)])
	}
}

// An ordinary batch name is untouched, so the bound is not paid by every sync.
func TestAnOrdinaryBatchNameIsReportedAsItIs(t *testing.T) {
	dir := t.TempDir()
	batch := filepath.Join(dir, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	const name = "deja-sync-9f2a1b-20260825T090000Z.jsonl"
	if err := os.WriteFile(filepath.Join(batch, name), []byte("{not a record}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Import(filepath.Join(dir, "index.db"), batch)
	if err == nil {
		t.Fatal("a batch of nonsense imported cleanly")
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("an ordinary name was altered: %q", err.Error())
	}
}

// The count and the list have to agree: one file whose name carries the
// separator used to read as three entries under a sentence that said one
// (#1847).
func TestOneFileCannotReadAsSeveral(t *testing.T) {
	dir := t.TempDir()
	batch := filepath.Join(dir, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	const name = "one.jsonl; two.jsonl; three.jsonl"
	if err := os.WriteFile(filepath.Join(batch, name), []byte("{not a record}\n"), 0o644); err != nil {
		t.Skipf("the filesystem refused the name: %v", err)
	}
	_, err := Import(filepath.Join(dir, "index.db"), batch)
	if err == nil {
		t.Fatal("a batch of nonsense imported cleanly")
	}
	msg := err.Error()
	if !strings.Contains(msg, "from 1 file") {
		t.Fatalf("the fixture did not produce the single-file sentence: %q", msg)
	}
	// One quoted name, whatever it contains.
	if strings.Count(msg, `"`) != 2 {
		t.Errorf("the name is not delimited, so it can read as several: %q", msg)
	}
	if !strings.Contains(msg, `"one.jsonl; two.jsonl; three.jsonl"`) {
		t.Errorf("the name was altered rather than delimited: %q", msg)
	}
}
