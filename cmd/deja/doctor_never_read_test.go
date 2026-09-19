package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The row says when a store holds a transcript the index has never read.
// Audited on a real store: ten of them, five written eight weeks earlier, and
// nothing anywhere said so — a session count of zero reads as "nothing written
// yet" rather than "five files never opened" (#3747).
func TestDoctorSaysWhenATranscriptWasNeverRead(t *testing.T) {
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
	write("one.jsonl", "aaa", "the pool ran dry under load")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, dir)
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "claude ") {
				return l
			}
		}
		t.Fatal("no claude row in the report")
		return ""
	}
	if got := row(); strings.Contains(got, "never read") {
		t.Fatalf("row = %q, want no such note while everything is read", got)
	}

	// A second transcript arrives and nothing has indexed it.
	write("two.jsonl", "bbb", "the cache eviction was wrong")
	got := row()
	if !strings.Contains(got, "1 transcript never read") {
		t.Fatalf("row = %q, want it to name the transcript never read", got)
	}
	// And what to do about it, since the reader cannot know that a search or a
	// recall is what adopts it.
	if !strings.Contains(got, "deja index") {
		t.Fatalf("row = %q, want the command that reads it", got)
	}

	// After a pass it goes quiet: a prompt, not a permanent complaint.
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := row(); strings.Contains(got, "never read") {
		t.Fatalf("row = %q after an index pass, want the note gone", got)
	}
}
