package sources

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A Cline prompt becomes the session's stored title, which every surface shows
// and sync carries to other machines. It was cut at 120 bytes, so a prompt in
// any non-ASCII language kept a broken byte forever (#1319). The padding moves
// the cut off whichever boundary it happened to land on.
func TestClineTitleCutsOnRuneBoundaries(t *testing.T) {
	for _, pad := range []string{"", "a", "ab"} {
		got := firstLineTrim(pad + strings.Repeat("ошибка подключения ", 20))
		if !utf8.ValidString(got) {
			t.Errorf("pad %q: the stored title is not valid UTF-8: %q", pad, got)
		}
		if len(got) > 120 {
			t.Errorf("pad %q: the title ran past its bound at %d bytes", pad, len(got))
		}
	}
}

// A short prompt is untouched, and the first line is still what names it.
func TestClineTitleKeepsShortPrompts(t *testing.T) {
	if got := firstLineTrim("fix the retry queue\nand then deploy"); got != "fix the retry queue" {
		t.Errorf("title = %q", got)
	}
}
