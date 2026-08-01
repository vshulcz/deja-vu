package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A harness that changes its layout presents as quietly fewer sessions, no
// error, and a directory size that still looks right — and doctor, the command
// that exists to rule that out, counted only what the parser already saw
// (#701).
func TestUnplacedFiles(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	seen := []string{mk("proj/chats/a.jsonl"), mk("proj/chats/b.jsonl")}
	mk("proj/stray.jsonl")
	mk("proj/other/deeper.json")
	// Not a transcript: extensions deja never reads must not be counted as
	// missed history.
	mk("proj/notes.md")
	mk("proj/db.sqlite")

	if got := unplacedFiles(root, seen); got != 2 {
		t.Errorf("unplaced = %d, want 2", got)
	}
	// Everything accounted for: nothing to report.
	all := append(seen, filepath.Join(root, "proj", "stray.jsonl"), filepath.Join(root, "proj", "other", "deeper.json"))
	if got := unplacedFiles(root, all); got != 0 {
		t.Errorf("unplaced = %d with every file seen", got)
	}
	// A root that does not exist is not a complaint.
	if got := unplacedFiles(filepath.Join(root, "nope"), nil); got != 0 {
		t.Errorf("missing root reported %d", got)
	}
	if got := unplacedFiles("", nil); got != 0 {
		t.Errorf("empty root reported %d", got)
	}
}

// The note is only worth printing when something is actually unaccounted for:
// ", 0 not recognised here" on every harness is noise.
func TestDoctorHarnessRowSaysNothingWhenEverythingIsPlaced(t *testing.T) {
	tmp := hermeticEnv(t)
	qwen := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(qwen, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qwen, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	var buf bytes.Buffer
	doctorHarnesses(&buf)
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "qwen") && strings.Contains(line, "not recognised") {
			t.Errorf("clean store complains: %q", line)
		}
	}

	// One file in the wrong place, and the row says so.
	if err := os.WriteFile(filepath.Join(tmp, "qwen", "projects", "proj", "stray.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctorHarnesses(&buf)
	found := false
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "qwen") && strings.Contains(line, "1 not recognised here") {
			found = true
		}
	}
	if !found {
		t.Errorf("stray file not reported:\n%s", buf.String())
	}
}
