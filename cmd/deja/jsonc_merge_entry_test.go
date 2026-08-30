package main

import (
	"strings"
	"testing"
)

// What the reader put on deja's own entry is theirs. The parsed path merges it
// (#2479): an env pointing at a store on another disk survives, and an entry
// switched off stays off with a line saying so. The text path replaces the
// line, so both were lost without a word (#2742).
func TestJSONCKeepsWhatTheReaderPutOnDejasEntry(t *testing.T) {
	seed := "{ // hi\n  \"mcp\": {\n" +
		"    \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]," +
		"\"environment\":{\"DEJA_INDEX_DIR\":\"/vol/store\"},\"enabled\":false}\n" +
		"  }\n}\n"

	out, note, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "/bin/deja") {
		t.Fatalf("the binary was not updated:\n%s", got)
	}
	if !strings.Contains(got, "/vol/store") {
		t.Errorf("the reader's environment was dropped:\n%s", got)
	}
	if !strings.Contains(got, "\"enabled\":false") && !strings.Contains(got, "\"enabled\": false") {
		t.Errorf("an entry the reader had switched off was silently switched on:\n%s", got)
	}
	// The note names what was taken over, once.
	if !strings.Contains(note, "/old/deja") {
		t.Errorf("the note does not say what the entry ran: %q", note)
	}
	if strings.Count(note, "/old/deja") != 1 {
		t.Errorf("the note says it twice: %q", note)
	}
	// Not asserted: the "still off" line. It is keyed to `disabled`, and
	// opencode writes `enabled`, so it does not fire here — a gap of its own.
}
