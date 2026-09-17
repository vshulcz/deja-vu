package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A wiring that names a deja which exists and is neither this one nor the one
// on PATH is a landmine: it works until that file goes, and the row said
// `wired` either way (#3656). And the check has to stay quiet about the
// ordinary case, or every row of a dev build's report carries a complaint.
func TestOtherBinaryNoteNamesOnlyAStrangePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	// The deja a user would recognise: the one on PATH.
	onPath := filepath.Join(dir, "deja")
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wiring := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(wiring, []byte(`{"command":"`+onPath+` hook-context"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if note := otherBinaryNote(wiring, "codex-auto"); note != "" {
		t.Errorf("the deja on PATH was reported as a stranger: %s", note)
	}

	// A build left in a scratch directory is the case worth naming.
	stray := filepath.Join(dir, "scratch", "deja")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiring, []byte(`{"command":"`+stray+` hook-context"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	note := otherBinaryNote(wiring, "codex-auto")
	if note == "" {
		t.Fatalf("a wiring pointing at %s was not reported", stray)
	}
	if !strings.Contains(note, stray) {
		t.Errorf("the note does not name the path: %s", note)
	}
	if !strings.Contains(note, "deja install codex-auto") {
		t.Errorf("the note does not say how to fix it: %s", note)
	}
}
