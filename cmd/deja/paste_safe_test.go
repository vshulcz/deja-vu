package main

import (
	"strings"
	"testing"
	"unicode"
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
		// A shell acts on more than quotes and spaces. Pasting the first of
		// these used to run `id`, and the second truncated a file.
		{"proj&&id", `'proj&&id'`},
		{"proj;id", `'proj;id'`},
		{"proj>out", `'proj>out'`},
		{"proj|id", `'proj|id'`},
		{"proj(x)", `'proj(x)'`},
		{"proj*", `'proj*'`},
		{"~proj", `'~proj'`},
		// C1 controls and the format characters: the prose half strips these,
		// so the command half cannot carry them raw. U+009B is CSI.
		{"proj\u009bX", `$'proj\u009bX'`},
		{"proj\u202eX", `$'proj\u202eX'`},
		// A byte that is not valid UTF-8 stays that byte: decoding it to
		// U+FFFD wrote three different bytes back, so the pasted command
		// named a key that does not exist.
		{"proj\xffX", `$'proj\xffX'`},
		// An ordinary accented name is not a shell problem and stays bare.
		{"projekt-über", "projekt-über"},
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

// The two halves of the line agree about what a terminal acts on: the prose
// half strips it, the command half quotes it, and neither passes it through.
func TestBothHalvesOfTheHintAgreeAboutControlCharacters(t *testing.T) {
	for _, id := range []string{
		"deja-2026-08-25-p\x1b[31mX",
		"deja-2026-08-25-p\u009bX",
		"deja-2026-08-25-p\u202eX",
		"deja-2026-08-25-p\u200eX",
		"deja-2026-08-25-p\x7fX",
	} {
		hint := tombstoneHint("it is written", id)
		for _, r := range hint {
			if unicode.IsControl(r) && r != '\n' {
				t.Errorf("%q: the hint carries a control character: %q", id, hint)
				break
			}
			if unicode.Is(unicode.Cf, r) {
				t.Errorf("%q: the hint carries a format character: %q", id, hint)
				break
			}
		}
	}
}

// The ANSI-C form is bash and zsh only, so the line says so — a dash or fish
// reader pasting it would get a literal `$` and match nothing.
func TestTheHintNamesTheShellsThatCanPasteIt(t *testing.T) {
	odd := tombstoneHint("it is written", "deja-2026-08-25-p\x1b[31mX")
	if !strings.Contains(odd, "in bash or zsh") {
		t.Errorf("the quoted form is not attributed to a shell that reads it:\n%s", odd)
	}
	plain := tombstoneHint("it is written", "deja-2026-08-25-notes")
	if strings.Contains(plain, "bash or zsh") {
		t.Errorf("an ordinary id was given a shell caveat it does not need:\n%s", plain)
	}
}
