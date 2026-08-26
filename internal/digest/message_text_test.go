package digest

import (
	"strings"
	"testing"
)

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
