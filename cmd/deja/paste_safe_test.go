package main

import (
	"strings"
	"testing"
)

// The hint hands the reader a command to paste, so the id in it cannot be
// sanitised — a sanitised id names nothing and the paste fails. It also cannot
// carry an escape byte to the terminal (#1794). ANSI-C quoting is both: the
// bytes are visible, and bash and zsh reproduce them.
func TestAPastedIdShowsItsControlBytesAndStillPastes(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"deja-2026-08-25-notes", "deja-2026-08-25-notes"},
		{"deja-2026-08-25-p\x1b[31mX", `$'deja-2026-08-25-p\e[31mX'`},
		{"two words", `'two words'`},
		{"it's", `'it'"'"'s'`},
		{"tab\there", `$'tab\there'`},
		{"nl\nhere", `$'nl\nhere'`},
		{"nul\x00here", `$'nul\x00here'`},
	} {
		if got := pasteSafe(c.in); got != c.want {
			t.Errorf("pasteSafe(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// And the hint itself carries no raw escape byte.
func TestTheTombstoneHintCarriesNoEscapeByte(t *testing.T) {
	hint := tombstoneHint("it is written", "deja-2026-08-25-p\x1b[31mX")
	if strings.ContainsRune(hint, 0x1b) {
		t.Errorf("the hint carries an escape byte to the terminal:\n%q", hint)
	}
	if !strings.Contains(hint, "deja forget --unforget") {
		t.Errorf("the hint no longer says how to lift the tombstone:\n%s", hint)
	}
	if !strings.Contains(hint, `\e[31mX`) {
		t.Errorf("the id is not shown the way it can be pasted back:\n%s", hint)
	}
}
