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

	if got, _ := unplacedFiles(root, seen, nil); got != 2 {
		t.Errorf("unplaced = %d, want 2", got)
	}
	// Everything accounted for: nothing to report.
	all := append(seen, filepath.Join(root, "proj", "stray.jsonl"), filepath.Join(root, "proj", "other", "deeper.json"))
	if got, _ := unplacedFiles(root, all, nil); got != 0 {
		t.Errorf("unplaced = %d with every file seen", got)
	}
	// A root that does not exist is not a complaint.
	if got, _ := unplacedFiles(filepath.Join(root, "nope"), nil, nil); got != 0 {
		t.Errorf("missing root reported %d", got)
	}
	if got, _ := unplacedFiles("", nil, nil); got != 0 {
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
	doctorHarnesses(&buf, t.TempDir())
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
	doctorHarnesses(&buf, t.TempDir())
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

// doctor's job is to rule out the tool failing to read the user's history, so
// its own count must not read as that failure. Everything deja did not parse
// was one number: on this machine that was "1192 not recognised here" for
// claude, of which 596 were subagent transcripts deja is written to leave
// alone, and "482" for codex, of which 452 sat in `.tmp`.
func TestUnplacedSeparatesDeliberateSkipsFromTheUnread(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	read := write("proj/read.jsonl")
	write("proj/session/subagents/agent-1.jsonl")
	write("proj/session/subagents/agent-2.jsonl")
	write(".tmp/scratch.json")
	write("proj/strange.jsonl")

	skipped := func(p string) bool { return filepath.Base(filepath.Dir(p)) == "subagents" }
	unread, byRule := unplacedFiles(root, []string{read}, skipped)
	if byRule != 2 {
		t.Errorf("deliberate skips counted as %d, want 2", byRule)
	}
	// Only the one file that is neither read nor skipped by a rule. The
	// dot-directory is a store's own scratch, not a transcript.
	if unread != 1 {
		t.Errorf("unread counted as %d, want 1", unread)
	}
}

// Without a rule the count behaves as it did, minus the scratch.
func TestUnplacedWithoutARuleStillCounts(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a.jsonl", "b.jsonl", ".cache/c.jsonl"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unread, byRule := unplacedFiles(root, nil, nil)
	if byRule != 0 {
		t.Errorf("skips reported without a rule: %d", byRule)
	}
	if unread != 2 {
		t.Errorf("unread counted as %d, want 2", unread)
	}
}
