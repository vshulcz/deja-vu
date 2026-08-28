package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The déjà vu line is rate-limited per session, and the session id comes from
// the hook payload. `.dejavu` is a line of two fields, so an id carrying
// whitespace was never matched — the line then fired on every prompt, which is
// the wallpaper the limit exists to prevent — and an id carrying a newline
// wrote a line under whatever followed it, spending another session's window
// (#2170).
func TestTheDejaVuLimitIsKeptPerSessionWhateverTheIDLooksLike(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir + ".dejavu"
	for _, sid := range []string{"plain-1", "two words", "forge\nvictim", "tab\tsep"} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if !dejaVuLineDue(dir, sid) {
			t.Errorf("session %q was refused its first line", sid)
		}
		if dejaVuLineDue(dir, sid) {
			t.Errorf("session %q is not rate-limited: it may say it again at once", sid)
		}
		// One entry per session, whatever its id looks like.
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(strings.Split(strings.TrimRight(string(body), "\n"), "\n")); n != 1 {
			t.Errorf("session %q wrote %d lines for one notice:\n%q", sid, n, body)
		}
		// And no other session's window was spent on it.
		for _, other := range strings.FieldsFunc(sid, func(r rune) bool {
			return r == ' ' || r == '\n' || r == '\t'
		}) {
			if other == sid {
				continue
			}
			if !dejaVuLineDue(dir, other) {
				t.Errorf("a session called %q was silenced by a payload calling itself %q", other, sid)
			}
		}
	}

	// The machine-wide fallback for a host that sends no id keeps its own
	// window: a session may not spend it by naming itself after the key.
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !dejaVuLineDue(dir, "") {
		t.Fatal("the no-id fallback was refused its first line")
	}
	if dejaVuLineDue(dir, "") {
		t.Error("the no-id fallback is not rate-limited")
	}
	if !dejaVuLineDue(dir, "-") {
		t.Error("a session calling itself \"-\" was silenced by the no-id fallback's window")
	}
}
