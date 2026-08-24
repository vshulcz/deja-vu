package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Both commands stopped at their limit without a word, so eight of thirteen
// read as thirteen — the misread #1608 fixed on the search screen, in the two
// places it was still true (#1632).
func TestHowSaysWhenItCutTheList(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 13; i++ {
		id := "0000000" + string(rune('a'+i)) + "-1111-4000-8000-d6e7f8a9b0c1"
		line := `{"type":"assistant","timestamp":"2026-07-0` + string(rune('1'+i%9)) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./... -run Case` + string(rune('a'+i)) + `"}}]}}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	if err := runHow(dir, []string{"go", "test", "--limit", "3"}, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Count(out, "$ go test") != 3 {
		t.Fatalf("expected three entries, got:\n%s", out)
	}
	if !strings.Contains(out, "13") || !strings.Contains(out, "--limit") {
		t.Errorf("the cut is silent — nothing says how many there were or how to see them:\n%s", out)
	}
	// The control: an uncut list says nothing extra.
	b.Reset()
	if err := runHow(dir, []string{"go", "test", "--limit", "20"}, &b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "--limit") {
		t.Errorf("a complete answer still advertised the flag:\n%s", b.String())
	}
}

// files caps the same way and was equally silent (#1632).
func TestFilesSaysWhenItCutTheList(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Real files under a real repository: files skips paths it cannot find on
	// this disk, and the cap is only reached once rows survive that.
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 6; i++ {
		name := filepath.Join(repo, "file"+string(rune('a'+i))+".go")
		if err := os.WriteFile(name, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, `{"type":"assistant","timestamp":"2026-07-01T10:0`+string(rune('0'+i))+`:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"`+name+`"}}]}}`)
	}
	lines = append(lines, `{"type":"user","timestamp":"2026-07-01T10:09:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"user","content":"the retry loop keeps firing"}}`)
	if err := os.WriteFile(filepath.Join(store, "aaaa0001.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	if err := runFiles(dir, []string{"retry", "--limit", "2"}, &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "--limit") {
		t.Errorf("files cut the list in silence:\n%s", b.String())
	}
	// The control: nothing cut, nothing said.
	b.Reset()
	if err := runFiles(dir, []string{"retry", "--limit", "50"}, &b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "--limit") {
		t.Errorf("a complete answer still advertised the flag:\n%s", b.String())
	}
}
