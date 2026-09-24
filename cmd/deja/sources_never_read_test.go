package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja sources` says when a store holds a transcript the index has never
// read, in the words doctor has used since #3747. It is the command people run
// first, and its session count reads as "nothing written yet" rather than
// "files never opened" — the gap doctor was taught to name and this row was
// not (#3752).
func TestSourcesSaysWhenATranscriptWasNeverRead(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	write := func(name, sid, text string) {
		t.Helper()
		p := filepath.Join(claude, "-p-app", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-09-01T10:00:00Z","cwd":"/p/app","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	row := func() string {
		t.Helper()
		out, err := captureRun(t, "sources")
		if err != nil {
			t.Fatal(err)
		}
		return storeLine(out, "claude")
	}

	write("one.jsonl", "aaa", "the pool ran dry under load")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := row(); strings.Contains(got, "never read") {
		t.Fatalf("row = %q, want no note while everything is read", got)
	}

	// A second transcript arrives and nothing has indexed it.
	write("two.jsonl", "bbb", "the cache eviction was wrong")
	got := row()
	if !strings.Contains(got, "1 transcript never read") {
		t.Fatalf("row = %q, want it to name the transcript never read", got)
	}
	if !strings.Contains(got, "deja index") {
		t.Fatalf("row = %q, want the command that reads it", got)
	}
	// Appended after the counts, so a script reading the leading fields of the
	// tab-separated row is not moved.
	if !strings.HasPrefix(got, "claude\t") || !strings.Contains(got, "sessions=") {
		t.Fatalf("row = %q, want the existing fields where they were", got)
	}

	// After a pass it goes quiet.
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := row(); strings.Contains(got, "never read") {
		t.Fatalf("row = %q after an index pass, want the note gone", got)
	}
}

// One command must not give two answers about one store: the words and the
// count are doctor's, so the two surfaces agree about the same transcripts.
func TestSourcesAndDoctorAgreeAboutUnreadTranscripts(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	p := filepath.Join(claude, "-p-app", "seed.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s0","timestamp":"2026-09-01T10:00:00Z","cwd":"/p/app","message":{"role":"user","content":"seed"}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(claude, "-p-app", name), []byte(strings.Replace(line, "s0", name, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	if got := storeLine(out, "claude"); !strings.Contains(got, "2 transcripts never read — `deja index`") {
		t.Fatalf("sources row = %q, want doctor's wording for two", got)
	}
}
