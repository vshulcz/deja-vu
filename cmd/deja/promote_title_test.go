package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/redact"
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
	// One heading, and it is the folded title rather than its first line.
	if n := strings.Count(got, "\n## "); n != 1 {
		t.Errorf("the block has %d headings:\n%s", n, got)
	}
	if !strings.Contains(got, "## pool sizing - state: rejected - source: someone else\n") {
		t.Errorf("the heading is not the whole title, folded:\n%s", got)
	}
}

// A credential can be written with its key word at the end of one field and its
// value at the start of the next, and the patterns for those allow a newline
// between the two. Redacting the fields apart lost exactly that, which is the
// one thing this file is not allowed to lose: it is written to be handed to
// someone else, under a line that says how much was masked.
func TestExportRedactsASecretSplitAcrossTheFields(t *testing.T) {
	for _, c := range []struct{ name, title, text, leaked string }{
		{"a password under its key", "the db password:", "hunter2hunter2hunter2A9", "hunter2hunter2hunter2A9"},
		{"a bearer token", "auth header Bearer", "abcdefghijklmnopqrstu", "abcdefghijklmnopqrstu"},
		{"a private key", "-----BEGIN PRIVATE KEY-----", "MIIBVgIBADANBgkqhkiG9w0\n-----END PRIVATE KEY-----\nand the rest of the note survives", "MIIBVgIBADANBgkqhkiG9w0"},
	} {
		path := filepath.Join(t.TempDir(), "notes.md")
		masked, err := exportPromoted(path, c.title, c.text, "claude:s9", "accepted", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := string(b)
		if strings.Contains(got, c.leaked) {
			t.Errorf("%s: the value went out in the clear:\n%s", c.name, got)
		}
		if masked == 0 {
			t.Errorf("%s: the export reported nothing masked", c.name)
		}
		// Masked, not deleted: a marker says something was taken out, and an
		// empty body says nothing at all.
		if !strings.Contains(got, redact.Marker) {
			t.Errorf("%s: the value left no marker behind:\n%s", c.name, got)
		}
		if strings.Contains(c.text, "survives") && !strings.Contains(got, "and the rest of the note survives") {
			t.Errorf("%s: the rest of the note went with it:\n%s", c.name, got)
		}
	}
}
