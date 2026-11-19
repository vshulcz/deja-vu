package digest

import (
	"strings"
	"testing"
)

// MessageText lost coverage when the shared display filter replaced its own
// ANSI stripper: the branches that survive a control sequence, a fenced block
// and the 16-line cap were only ever reached through other packages' tests.
func TestMessageTextBranches(t *testing.T) {
	if got := MessageText("  \x1b[31m  \x1b[0m  "); got != "" {
		t.Errorf("a message that is only escapes and spaces should distil to nothing, got %q", got)
	}
	fenced := "here is the fix\n```go\nfunc main() {}\n```"
	if got := MessageText(fenced); got != fenced {
		t.Errorf("a fenced block must survive whole:\n%q", got)
	}
	// The cap keeps the first sixteen prose lines and drops the rest.
	long := ""
	var longSb18 strings.Builder
	for range 30 {
		longSb18.WriteString("this is a sentence of ordinary prose about pooling\n")
	}
	long += longSb18.String()
	got := MessageText(long)
	if n := len(got); n == 0 {
		t.Fatal("prose distilled to nothing")
	}
	if want := 16 * len("this is a sentence of ordinary prose about pooling"); len(got) > want+16 {
		t.Errorf("the 16-line cap did not hold: %d bytes", len(got))
	}
	// A control character inside prose is replaced, not carried through.
	if got := MessageText("pool \x07 exhausted"); got == "pool \x07 exhausted" {
		t.Errorf("the bell survived the filter: %q", got)
	}
}

// `MessageText` is the third place served text is stripped, after
// `search.SafeText` and `redact.SafeForDisplay` — the one the handoff digest
// is built through. This holds it directly; `TestNothingServedCarriesAControlByte`
// holds the path (#1985, #1989).
func TestMessageTextStripsWhatATerminalActsOn(t *testing.T) {
	in := "the build failed: \x1b[31mERROR\x1b[0m\x07 pgbouncer pool timed out\r and retried"

	got := MessageText(in)
	if got == "" {
		t.Fatal("the message came back empty, so this proves nothing")
	}
	if !strings.Contains(got, "pgbouncer") {
		t.Fatalf("stripping took the text with it: %q", got)
	}
	for i, r := range got {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("MessageText kept 0x%02x at %d: %q", r, i, got)
			break
		}
	}
}
