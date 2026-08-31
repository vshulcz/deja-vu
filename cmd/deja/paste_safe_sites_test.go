package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// carriesControl reports whether a printed line would reach the terminal with
// something on it that a terminal acts on.
func carriesControl(s string) bool {
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n') || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

// Every line that hands the reader a command to paste puts the value in it
// through pasteSafe: raw, an escape byte in a file name, a session id or the
// reader's own query reaches the terminal, and a shell metacharacter makes the
// pasted command run something else (#2768, the rest of #1794).
func TestTheHintsThatOfferACommandQuoteWhatTheyOffer(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// A metacharacter in the name, which is what reaches this hint: a control
	// byte does not, because the indexer drops the record before it gets here
	// — that half is held at the unit level below.
	path := "/api/internal/x/parser&&x.go"
	touched := `{"type":"assistant","timestamp":"2026-07-10T10:00:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` +
		path + `","old_string":"a","new_string":"b"}}]}}`
	ran := `{"type":"assistant","timestamp":"2026-07-10T10:05:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/x/... -run Parser"}}]}}`
	if err := os.WriteFile(filepath.Join(store, "one.jsonl"), []byte(touched+"\n"+ran+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A command is worth remembering once two sessions ran it, which is what
	// the table the `deja how` half reads is built from.
	again := strings.ReplaceAll(ran, "aaaa0001", "bbbb0002")
	if err := os.WriteFile(filepath.Join(store, "two.jsonl"), []byte(again+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The file half, end to end: the name comes out of a transcript and the
	// command below the sentence is meant to be pasted.
	hint := roleServedHint(dir, "parser")
	if !strings.Contains(hint, "deja blame") {
		t.Fatalf("the file half of the hint is missing, so this test guards nothing:\n%s", hint)
	}
	if carriesControl(hint) {
		t.Errorf("a control byte reached the terminal:\n%q", hint)
	}
	if strings.Contains(hint, "blame parser&&x.go") {
		t.Errorf("a shell metacharacter is unquoted in a command to paste:\n%q", hint)
	}
	// And the words half, through the hint itself: `deja how` ANDs its
	// arguments, so a quoted phrase becomes one term it then requires
	// contiguously — the count in the sentence above was taken over the words
	// (#2768). Asserted on the wiring, because the wiring is what decides
	// whether the offer and the count can disagree.
	hint = roleServedHint(dir, "parser test")
	if !strings.Contains(hint, "deja how") {
		t.Fatalf("the command half of the hint is missing, so this half guards nothing:\n%s", hint)
	}
	if !strings.Contains(hint, "`deja how parser test`") {
		t.Errorf("the words were not handed over as they were counted:\n%q", hint)
	}
}

// The command the file hook offers carries a path that came out of a
// transcript, which is the same kind of value.
func TestTheFileHookQuotesThePathItOffers(t *testing.T) {
	for _, name := range []string{"esc" + string(rune(0x1b)) + "[31mX.go", "amp&&id.go"} {
		line := fileHookBlameOffer("head", name)
		if carriesControl(line) {
			t.Errorf("%q: a control byte reached the terminal: %q", name, line)
		}
		if strings.Contains(line, "blame "+name) {
			t.Errorf("%q: the path is unquoted in a command to paste: %q", name, line)
		}
	}
}

// The `deja fix` line offers an error string lifted from a transcript, and a
// shell expands `$(…)` and backticks inside double quotes as readily as
// outside them — so Go's own `%q` was not quoting for the reader who pastes.
func TestTheFixHintQuotesTheErrorItOffers(t *testing.T) {
	for _, near := range []string{"error: $(rm -rf /tmp/x) failed", "error: `id` not found"} {
		got := pasteSafe(near)
		if strings.HasPrefix(got, `"`) {
			t.Errorf("%q was double-quoted, which a shell still expands: %s", near, got)
		}
		if !strings.HasPrefix(got, "'") && !strings.HasPrefix(got, "$'") {
			t.Errorf("%q was handed over unquoted: %s", near, got)
		}
	}
}

// A query word can start with a dash — somebody asking about `-run` or
// `--limit` — and `deja how` reads it as a flag it does not have, or as one it
// does, swallowing the next word. The command's own escape says the rest is
// the query.
func TestTheHowOfferEscapesAQueryThatLooksLikeFlags(t *testing.T) {
	for _, c := range []struct {
		terms []string
		want  string
	}{
		{[]string{"go", "test"}, "`deja how go test`"},
		{[]string{"-run", "parser"}, "`deja how -- -run parser`"},
		{[]string{"why", "--limit", "ignored"}, "`deja how -- why --limit ignored`"},
	} {
		got := howOfferLine(3, "match", c.terms)
		if !strings.Contains(got, c.want) {
			t.Errorf("howOfferLine(%v) = %q, want it to carry %s", c.terms, got, c.want)
		}
	}
}

// The line promote prints when it takes a mark back offers the note's id, and
// the same sentence echoes the session it came from: one is pasted, the other
// is read.
func TestTheTakenBackLineQuotesTheIdItOffers(t *testing.T) {
	line := markTakenBack("claude:has space", "accepted", sources.Lifecycle{State: "rejected"})
	if line == "" {
		t.Fatal("the fixture no longer produces the line this test is about")
	}
	if strings.Contains(line, "`deja show deja-note-claude-has space`") {
		t.Errorf("the id is unquoted in a command to paste: %q", line)
	}
	esc := string(rune(0x1b))
	line = markTakenBack("claude:esc"+esc+"[31mX", "accepted", sources.Lifecycle{State: "rejected"})
	if carriesControl(line) {
		t.Errorf("a control byte reached the terminal: %q", line)
	}
}
