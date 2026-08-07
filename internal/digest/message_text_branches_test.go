package digest

import "testing"

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
	for i := 0; i < 30; i++ {
		long += "this is a sentence of ordinary prose about pooling\n"
	}
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
