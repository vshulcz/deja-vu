package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The incremental path re-reads a file from where it stopped. Resuming from
// the wrong offset either loses the first message after the boundary or
// duplicates one, and both are silent.
func TestQwenParsesFromAnOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	first := `{"type":"user","sessionId":"q1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","parts":[{"text":"FIRSTLINE"}]}}` + "\n"
	second := `{"type":"user","sessionId":"q1","timestamp":"2026-01-01T00:01:00Z","message":{"role":"user","parts":[{"text":"SECONDLINE"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := ParseQwenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || len(all[0].Messages) != 2 {
		t.Fatalf("full parse returned %+v", all)
	}
	// Resuming past the first line must yield only what came after it.
	tail, err := ParseQwenFileFromOffset(path, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range tail {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "FIRSTLINE") {
				t.Fatalf("offset parse re-read an indexed line: %+v", s.Messages)
			}
		}
	}
	found := false
	for _, s := range tail {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "SECONDLINE") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("offset parse lost the message it was supposed to pick up")
	}
	// An offset past the end is what a truncated file looks like: nothing to
	// add, and no error for the caller to handle.
	if ss, err := ParseQwenFileFromOffset(path, 1<<20); err != nil || len(ss) != 0 {
		t.Fatalf("offset past the end: %+v %v", ss, err)
	}
}

func TestNotesParseFromOffsetSkipsWhatWasRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	first := `{"kind":"note","project":"p","text":"FIRSTNOTE","t":"2026-01-01T00:00:00Z"}` + "\n"
	second := `{"kind":"note","project":"p","text":"SECONDNOTE","t":"2026-01-01T00:01:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	tail, err := ParseNotesFileFromOffset(path, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range tail {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "FIRSTNOTE") {
				t.Fatalf("offset parse re-read a note: %+v", s.Messages)
			}
		}
	}
}
