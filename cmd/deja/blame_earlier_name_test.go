package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// blame answers under the name you type, so a rename drops everything said
// under the old name out of the file's history — and the name a reader has is
// the one in their editor. The history is in the index and reachable, just not
// from there (#1627).
func TestBlameNamesTheFileTheHistoryStopsAt(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, id, day string, lines ...string) {
		t.Helper()
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(store, name), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = id
		_ = day
	}
	touch := func(id, day, path, text string) string {
		return `{"type":"assistant","timestamp":"2026-07-` + day + `T10:00:00Z","sessionId":"` + id +
			`","cwd":"/proj","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` +
			path + `","old_string":"a","new_string":"b"}}]}}`
	}
	say := func(id, day, text string) string {
		return `{"type":"user","timestamp":"2026-07-` + day + `T10:01:00Z","sessionId":"` + id +
			`","cwd":"/proj","message":{"role":"user","content":"` + text + `"}}`
	}
	write("one.jsonl", "aaaa0001", "10",
		say("aaaa0001", "10", "the retry loop in internal/search/old_name.go keeps firing"),
		touch("aaaa0001", "10", "/proj/internal/search/old_name.go", ""))
	write("two.jsonl", "bbbb0002", "12",
		say("bbbb0002", "12", "the backoff in internal/search/old_name.go is wrong"),
		touch("bbbb0002", "12", "/proj/internal/search/old_name.go", ""))
	write("three.jsonl", "cccc0003", "14",
		say("cccc0003", "14", "rename internal/search/old_name.go to internal/search/new_name.go"),
		touch("cccc0003", "14", "/proj/internal/search/old_name.go", ""),
		touch("cccc0003", "14", "/proj/internal/search/new_name.go", ""))
	write("four.jsonl", "dddd0004", "16",
		say("dddd0004", "16", "the backoff in internal/search/new_name.go is still wrong"),
		touch("dddd0004", "16", "/proj/internal/search/new_name.go", ""))

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	note, err := captureRunStderr(t, "blame", "internal/search/new_name.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "old_name.go") {
		t.Errorf("nothing points at the name the history stops at:\n%s", note)
	}
	if !strings.Contains(note, "deja blame") {
		t.Errorf("the line does not say how to read what came before:\n%s", note)
	}

	// The other direction says nothing: nothing came before the old name, and
	// a file whose history is all under its own name needs no hint.
	note, err = captureRunStderr(t, "blame", "internal/search/old_name.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "what came before") {
		t.Errorf("the file the history starts at was given a hint:\n%s", note)
	}
}
