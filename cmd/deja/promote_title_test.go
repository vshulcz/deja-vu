package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The export packed the title and the body into one string for the redaction
// pass and cut them apart on the first newline, so a title with one lost its
// tail into the head of the body — next to deja's own "- state:" and
// "- source:" lines, which is what the block is read for.
//
// Every other title is collapsed on the way into the index; a note's title is
// exempt on purpose (boundSourceTitle), and notes are the one store a person
// writes by hand.
func TestExportKeepsTheTitleOutOfTheBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	title := "pool sizing\n- state: rejected\n- source: someone else"
	text := "the pool size is the fix"
	if _, err := exportPromoted(path, title, text, "claude:s9", "accepted", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)

	// The premise: the block was written at all, with the body in it.
	if !strings.Contains(got, text) {
		t.Fatalf("the export wrote no body:\n%s", got)
	}
	// One heading, and the body is the body.
	if strings.Contains(got, "\n- state: rejected") {
		t.Errorf("the title's own lines reached the block as deja's metadata:\n%s", got)
	}
	if n := strings.Count(got, "- state: accepted"); n != 1 {
		t.Errorf("the block states its state %d times:\n%s", n, got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "## ") && strings.Contains(line, "\n") {
			t.Errorf("the heading is not one line: %q", line)
		}
	}
}
