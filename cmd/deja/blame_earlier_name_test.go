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
	write := func(name string, lines ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(store, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch := func(id, day, path string) string {
		return `{"type":"assistant","timestamp":"2026-07-` + day + `T10:00:00Z","sessionId":"` + id +
			`","cwd":"/proj","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` +
			path + `","old_string":"a","new_string":"b"}}]}}`
	}
	say := func(id, day, text string) string {
		return `{"type":"user","timestamp":"2026-07-` + day + `T10:01:00Z","sessionId":"` + id +
			`","cwd":"/proj","message":{"role":"user","content":"` + text + `"}}`
	}
	write("one.jsonl",
		say("aaaa0001", "10", "the retry loop in internal/search/old_name.go keeps firing"),
		touch("aaaa0001", "10", "/proj/internal/search/old_name.go"))
	write("two.jsonl",
		say("bbbb0002", "12", "the backoff in internal/search/old_name.go is wrong"),
		touch("bbbb0002", "12", "/proj/internal/search/old_name.go"))
	write("three.jsonl",
		say("cccc0003", "14", "rename internal/search/old_name.go to internal/search/new_name.go"),
		touch("cccc0003", "14", "/proj/internal/search/old_name.go"),
		touch("cccc0003", "14", "/proj/internal/search/new_name.go"))
	write("four.jsonl",
		say("dddd0004", "16", "the backoff in internal/search/new_name.go is still wrong"),
		touch("dddd0004", "16", "/proj/internal/search/new_name.go"))
	// Two files that merely coincided once, a test file beside its subject, a
	// same-named file in another package, and a file from another project:
	// none of them is a name this file's history continues from, and each of
	// them fired before the rules were narrowed.
	write("five.jsonl",
		say("eeee0005", "11", "touch both while wiring the cache"),
		touch("eeee0005", "11", "/proj/internal/other/parser.go"),
		touch("eeee0005", "11", "/proj/internal/other/cache.go"))
	write("six.jsonl",
		say("ffff0006", "13", "the cache still misses"),
		touch("ffff0006", "13", "/proj/internal/other/cache.go"))
	write("seven.jsonl",
		say("gggg0007", "11", "write the table test"),
		touch("gggg0007", "11", "/proj/internal/tbl/foo.go"),
		touch("gggg0007", "11", "/proj/internal/tbl/foo_test.go"))
	write("eight.jsonl",
		say("hhhh0008", "13", "the table test needs another case"),
		touch("hhhh0008", "13", "/proj/internal/tbl/foo_test.go"))
	// A move out of another directory, worded as a rename: a name that moved
	// house is not the name this file's history continues under, and pointing
	// at it is how the note would start naming vendored copies.
	write("nine.jsonl",
		say("iiii0009", "15", "rename internal/deep/gone.go to internal/search/new_name.go"),
		touch("iiii0009", "15", "/proj/internal/deep/gone.go"),
		touch("iiii0009", "15", "/proj/internal/search/new_name.go"))
	// And a refactor that swept several files along while saying the word:
	// whichever of them stopped last would be named on the strength of the
	// sweep rather than of anything about it.
	write("ten.jsonl",
		say("jjjj0010", "15", "rename internal/search/swept.go to internal/search/new_name.go while we are here"),
		touch("jjjj0010", "15", "/proj/internal/search/swept.go"),
		touch("jjjj0010", "15", "/proj/internal/search/a.go"),
		touch("jjjj0010", "15", "/proj/internal/search/b.go"),
		touch("jjjj0010", "15", "/proj/internal/search/c.go"),
		touch("jjjj0010", "15", "/proj/internal/search/new_name.go"))

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
	// What was seen, not what it means: deja cannot tell a rename from a file
	// that was deleted while this one took over its work.
	if strings.Contains(note, "renamed") {
		t.Errorf("the line claims a rename it cannot know about:\n%s", note)
	}
	// One name, and it is the one in the same directory, from the session that
	// was about the move rather than one that swept it along.
	for _, other := range []string{"gone.go", "swept.go", "a.go"} {
		if strings.Contains(note, other) {
			t.Errorf("the note named %s:\n%s", other, note)
		}
	}

	// The other direction says nothing: nothing came before the old name, and
	// a file whose history is all under its own name needs no hint.
	note, err = captureRunStderr(t, "blame", "internal/search/old_name.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "continues from") {
		t.Errorf("the file the history starts at was given a hint:\n%s", note)
	}

	// And the cases that are not a rename by any reading: a pair that met
	// once, and a test file beside its subject.
	for _, file := range []string{"internal/other/cache.go", "internal/tbl/foo_test.go"} {
		note, err = captureRunStderr(t, "blame", file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(note, "continues from") {
			t.Errorf("%s was told its history continues from a file it merely shared a session with:\n%s", file, note)
		}
	}
}
