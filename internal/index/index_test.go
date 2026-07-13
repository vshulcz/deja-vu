package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestIndexIngestSkipAndSearch(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"fast opencode needle"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"answer text"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	var first bytes.Buffer
	if err := Ensure(dir, "claude", false, &first); err != nil {
		t.Fatal(err)
	}
	if first.Len() == 0 {
		t.Fatal("first build did not print progress")
	}
	var second bytes.Buffer
	if err := Ensure(dir, "claude", false, &second); err != nil {
		t.Fatal(err)
	}
	if second.Len() != 0 {
		t.Fatalf("fresh index rebuilt unexpectedly: %q", second.String())
	}
	ss, err := Search(dir, search.Options{Query: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" || ss[0].Messages[0].Text != "fast opencode needle" {
		t.Fatalf("bad search sessions: %#v", ss)
	}
}

func TestIncrementalOnlyReingestsChangedFile(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := filepath.Join(proj, "s1.jsonl")
	s2 := filepath.Join(proj, "s2.jsonl")
	if err := os.WriteFile(s1, []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"alpha needle"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s2, []byte(`{"type":"user","sessionId":"s2","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"beta stable"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s1, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:06:05Z","message":{"role":"assistant","content":"gamma appended"}}` + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(s1, now, now)
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "changed_files=1") {
		t.Fatalf("incremental log missing changed_files=1: %q", log.String())
	}
	ss, err := Search(dir, search.Options{Query: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s2" {
		t.Fatalf("unchanged file was not preserved: %#v", ss)
	}
	ss, err = Search(dir, search.Options{Query: "appended"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("changed file was not reingested: %#v", ss)
	}
}
