package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Two stores, one row. The file list carries the CLI database as well as the
// transcripts, and the transcript reader answers zero for a database — so the
// store probe has to be handed the transcripts alone or the row reports
// `parsed-zero` about a store whose sessions deja has just indexed (#3675).
func TestZCodeRowCountsTranscriptsAndNamesTheDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".zcode", "projects", "--work--")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"role":"user","content":"hello","timestamp":"2026-09-17T10:00:00Z","sessionId":"s1"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	db := sources.ZCodeDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	// The file being there is what the row reports; its contents are the
	// reader's business and have their own test in internal/sources.
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := len(sources.ZCodeTranscriptFiles()); got != 1 {
		t.Fatalf("transcripts = %d, want 1 — the database must not be in that list", got)
	}
	named := false
	for _, p := range sources.ZCodeSessionFiles() {
		if p == db {
			named = true
		}
	}
	if !named {
		t.Error("the ingest file list does not name the database")
	}

	var out bytes.Buffer
	doctorHarnesses(&out, t.TempDir())
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "zcode ") {
			continue
		}
		if strings.Contains(line, "parsed-zero") {
			t.Errorf("the row calls a readable store broken: %s", line)
		}
		if !strings.Contains(line, "CLI store present") {
			t.Errorf("the row does not say the database is there: %s", line)
		}
		return
	}
	t.Fatalf("no zcode row:\n%s", out.String())
}
