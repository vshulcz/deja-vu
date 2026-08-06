package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Transcript text arrives verbatim from a harness, and after `deja sync
// import` from another machine. An escape byte recolours the screen, erases
// the line above or sets the window title; a bell rings on every redraw. The
// status bar and the brief titles have stripped these for releases; the
// reading surfaces printed them raw (#1090).
const escProbe = "probe \x1b[31mRED\x1b[0m \x1b[2K \x1b[1A \x1b]0;pwned\x07 \x00 end"

func assertNoTerminalControls(t *testing.T, what string, out []byte) {
	t.Helper()
	for _, c := range []struct {
		b    byte
		name string
	}{{0x1b, "ESC"}, {0x07, "BEL"}, {0x00, "NUL"}} {
		if bytes.IndexByte(out, c.b) >= 0 {
			t.Errorf("%s passed %s through to the terminal:\n%q", what, c.name, out)
		}
	}
}

func TestSafeTextNeutralisesControls(t *testing.T) {
	got := SafeText(escProbe)
	assertNoTerminalControls(t, "SafeText", []byte(got))
	// The words either side survive, and separately: a control that vanished
	// would run "RED" into what follows it.
	for _, want := range []string{"probe", "RED", "pwned", "end"} {
		if !strings.Contains(got, want) {
			t.Errorf("SafeText dropped %q: %q", want, got)
		}
	}
	// The session's own layout is not a terminal attack.
	if got := SafeText("one\ntwo\tthree"); got != "one\ntwo\tthree" {
		t.Errorf("newline or tab was rewritten: %q", got)
	}
	// A bidi override reverses the rendering of what follows: the same
	// display attack without an escape byte.
	if got := SafeText("safe \u202ereversed\u202c tail"); strings.ContainsAny(got, "\u202e\u202c") {
		t.Errorf("a bidi override survived: %q", got)
	}
	// Ordinary text is returned untouched, including emoji held together by a
	// zero-width joiner.
	for _, s := range []string{"plain text", "family 👨‍👩‍👧", "日本語", "لغة عربية"} {
		if SafeText(s) != s {
			t.Errorf("SafeText altered ordinary text %q -> %q", s, SafeText(s))
		}
	}
}

func TestPrintingSurfacesStripControls(t *testing.T) {
	s := model.Session{
		ID: "s-esc", Harness: "claude", Project: "tmp/esc",
		Updated:  time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		Messages: []model.Message{{Role: "user", Text: escProbe, Time: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)}},
	}

	var out bytes.Buffer
	Print(&out, []Hit{{Session: s, Count: 1, Snippets: []string{escProbe}}}, Options{Query: "probe"})
	assertNoTerminalControls(t, "search", out.Bytes())

	out.Reset()
	PrintSession(&out, s)
	assertNoTerminalControls(t, "show", out.Bytes())
	if !strings.Contains(out.String(), "RED") {
		t.Errorf("show lost the message body:\n%s", out.String())
	}

	out.Reset()
	PrintContext(&out, s, "probe")
	assertNoTerminalControls(t, "ctx", out.Bytes())
}
