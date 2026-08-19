// Package termwidth measures how wide text prints in a terminal cell grid.
//
// Two surfaces lay text out against a width they cannot query per-character:
// the search result line and the status bar. Both used to count runes, which
// is right for Latin text and wrong by a factor of two for CJK — a Chinese
// line was cut to twice the terminal's width, so the text was lost to the edge
// rather than reflowed.
package termwidth

// Columns is how wide a string prints. A CJK character is one rune and two
// columns.
func Columns(s string) int {
	n := 0
	for _, r := range s {
		n += RuneColumns(r)
	}
	return n
}

// RuneColumns is the East Asian Wide and Fullwidth ranges, plus the emoji
// blocks terminals render double. Everything else counts as one: this is a
// layout budget, not a Unicode implementation, and being wrong by a column on
// an unusual rune costs a wrap rather than data.
func RuneColumns(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, Kangxi
		r >= 0x3041 && r <= 0x33FF, // kana, Hangul compatibility, CJK symbols
		r >= 0x3400 && r <= 0x4DBF, // CJK extension A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified ideographs
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD: // CJK extensions B and beyond
		return 2
	}
	return 1
}

// Cut keeps as much of s as fits in width columns.
func Cut(s string, width int) string {
	n := 0
	for i, r := range s {
		w := RuneColumns(r)
		if n+w > width {
			return s[:i]
		}
		n += w
	}
	return s
}
