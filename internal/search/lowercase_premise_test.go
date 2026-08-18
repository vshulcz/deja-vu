package search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// snippet finds a match in the lowercased text and cuts the original, carrying
// the position across as a rune index. That is only sound because lowercasing in
// Go maps one rune to one rune — it is the simple case mapping, not Unicode's
// full one, so nothing expands the way "ﬁ" or a Turkish İ would under a locale
// aware mapping. The premise is cheap to check and would break silently, so it
// is checked.
func TestToLowerKeepsRuneCount(t *testing.T) {
	bad := 0
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		s := string(r)
		if n := utf8.RuneCountInString(strings.ToLower(s)); n != 1 {
			if bad < 10 {
				t.Errorf("U+%04X lowercases to %d runes", r, n)
			}
			bad++
		}
	}
	t.Logf("runes whose lowercase is not one rune: %d", bad)

	// Invalid UTF-8 too: bytes that are not runes at all.
	for _, s := range []string{"\xff\xfe", "a\x80b", "\xed\xa0\x80"} {
		if utf8.RuneCountInString(strings.ToLower(s)) != utf8.RuneCountInString(s) {
			t.Errorf("invalid input %q changed rune count under ToLower", s)
		}
	}
}
