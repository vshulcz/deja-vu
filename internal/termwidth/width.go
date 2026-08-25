// Package termwidth measures how wide text prints in a terminal cell grid.
//
// Two surfaces lay text out against a width they cannot query per-character:
// the search result line and the status bar. Both used to count runes, which
// is right for Latin text and wrong by a factor of two for CJK — a Chinese
// line was cut to twice the terminal's width, so the text was lost to the edge
// rather than reflowed.
package termwidth

import "github.com/vshulcz/deja-vu/internal/nfcfold"

// Columns is how wide a string prints. A CJK character is one rune and two
// columns.
//
// The string is composed first, so the same name measures the same however it
// was typed: macOS hands paths back decomposed and project names come from
// paths, so "über-server" measured 12 columns there and 11 everywhere else, and
// every aligned screen padded it a column short (#1824). Composing is a scan
// and no allocation for text with no combining mark in it, which is nearly all
// of what these screens print.
func Columns(s string) int {
	n := 0
	for _, r := range nfcfold.Compose(s) {
		n += RuneColumns(r)
	}
	return n
}

// RuneColumns is how many cells a rune takes: two for everything East Asian
// Width calls Wide or Fullwidth, one for everything else.
//
// The table used to be a handful of hand-written blocks, and every gap in it
// was a line that ran past the edge by exactly one column per rune — 🚀 was
// missing, then ✅ ✨ ❌ ⚡ ⭐ 🆗 🟰 were missing from the block that replaced
// it (#1594). Patching block by block kept losing to the next emoji someone
// typed, so the ranges below are the Wide and Fullwidth set from the Unicode
// character database, generated rather than guessed. It is complete as of
// Unicode 15.0: emoji added later count one column until the table is
// regenerated, which narrows the failure from "the next emoji someone types" to
// "the next Unicode release".
//
// Combining marks are counted as the table finds them, which means the CJK ones
// (U+3099, U+302A) count two even though they render on the base character —
// `か` plus U+3099 measures four columns and draws two. This is a layout budget,
// not a shaping engine, and over-counting shortens a line rather than running
// it past the edge; the same was true of the hand-written table this replaced.
func RuneColumns(r rune) int {
	if r < wideRanges[0].lo {
		return 1
	}
	lo, hi := 0, len(wideRanges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < wideRanges[mid].lo:
			hi = mid - 1
		case r > wideRanges[mid].hi:
			lo = mid + 1
		default:
			return 2
		}
	}
	return 1
}

// wideRanges is every East Asian Wide and Fullwidth range, sorted. Generated
// from the Unicode character database (15.0):
//
//	python3 -c 'import unicodedata; ...east_asian_width(chr(cp)) in ("W","F")'
var wideRanges = [...]struct{ lo, hi rune }{
	{0x1100, 0x115F},
	{0x231A, 0x231B},
	{0x2329, 0x232A},
	{0x23E9, 0x23EC},
	{0x23F0, 0x23F0},
	{0x23F3, 0x23F3},
	{0x25FD, 0x25FE},
	{0x2614, 0x2615},
	{0x2648, 0x2653},
	{0x267F, 0x267F},
	{0x2693, 0x2693},
	{0x26A1, 0x26A1},
	{0x26AA, 0x26AB},
	{0x26BD, 0x26BE},
	{0x26C4, 0x26C5},
	{0x26CE, 0x26CE},
	{0x26D4, 0x26D4},
	{0x26EA, 0x26EA},
	{0x26F2, 0x26F3},
	{0x26F5, 0x26F5},
	{0x26FA, 0x26FA},
	{0x26FD, 0x26FD},
	{0x2705, 0x2705},
	{0x270A, 0x270B},
	{0x2728, 0x2728},
	{0x274C, 0x274C},
	{0x274E, 0x274E},
	{0x2753, 0x2755},
	{0x2757, 0x2757},
	{0x2795, 0x2797},
	{0x27B0, 0x27B0},
	{0x27BF, 0x27BF},
	{0x2B1B, 0x2B1C},
	{0x2B50, 0x2B50},
	{0x2B55, 0x2B55},
	{0x2E80, 0x2E99},
	{0x2E9B, 0x2EF3},
	{0x2F00, 0x2FD5},
	{0x2FF0, 0x2FFB},
	{0x3000, 0x303E},
	{0x3041, 0x3096},
	{0x3099, 0x30FF},
	{0x3105, 0x312F},
	{0x3131, 0x318E},
	{0x3190, 0x31E3},
	{0x31F0, 0x321E},
	{0x3220, 0x3247},
	{0x3250, 0x4DBF},
	{0x4E00, 0xA48C},
	{0xA490, 0xA4C6},
	{0xA960, 0xA97C},
	{0xAC00, 0xD7A3},
	{0xF900, 0xFAFF},
	{0xFE10, 0xFE19},
	{0xFE30, 0xFE52},
	{0xFE54, 0xFE66},
	{0xFE68, 0xFE6B},
	{0xFF01, 0xFF60},
	{0xFFE0, 0xFFE6},
	{0x16FE0, 0x16FE4},
	{0x16FF0, 0x16FF1},
	{0x17000, 0x187F7},
	{0x18800, 0x18CD5},
	{0x18D00, 0x18D08},
	{0x1AFF0, 0x1AFF3},
	{0x1AFF5, 0x1AFFB},
	{0x1AFFD, 0x1AFFE},
	{0x1B000, 0x1B122},
	{0x1B132, 0x1B132},
	{0x1B150, 0x1B152},
	{0x1B155, 0x1B155},
	{0x1B164, 0x1B167},
	{0x1B170, 0x1B2FB},
	{0x1F004, 0x1F004},
	{0x1F0CF, 0x1F0CF},
	{0x1F18E, 0x1F18E},
	{0x1F191, 0x1F19A},
	{0x1F200, 0x1F202},
	{0x1F210, 0x1F23B},
	{0x1F240, 0x1F248},
	{0x1F250, 0x1F251},
	{0x1F260, 0x1F265},
	{0x1F300, 0x1F320},
	{0x1F32D, 0x1F335},
	{0x1F337, 0x1F37C},
	{0x1F37E, 0x1F393},
	{0x1F3A0, 0x1F3CA},
	{0x1F3CF, 0x1F3D3},
	{0x1F3E0, 0x1F3F0},
	{0x1F3F4, 0x1F3F4},
	{0x1F3F8, 0x1F43E},
	{0x1F440, 0x1F440},
	{0x1F442, 0x1F4FC},
	{0x1F4FF, 0x1F53D},
	{0x1F54B, 0x1F54E},
	{0x1F550, 0x1F567},
	{0x1F57A, 0x1F57A},
	{0x1F595, 0x1F596},
	{0x1F5A4, 0x1F5A4},
	{0x1F5FB, 0x1F64F},
	{0x1F680, 0x1F6C5},
	{0x1F6CC, 0x1F6CC},
	{0x1F6D0, 0x1F6D2},
	{0x1F6D5, 0x1F6D7},
	{0x1F6DC, 0x1F6DF},
	{0x1F6EB, 0x1F6EC},
	{0x1F6F4, 0x1F6FC},
	{0x1F7E0, 0x1F7EB},
	{0x1F7F0, 0x1F7F0},
	{0x1F90C, 0x1F93A},
	{0x1F93C, 0x1F945},
	{0x1F947, 0x1F9FF},
	{0x1FA70, 0x1FA7C},
	{0x1FA80, 0x1FA88},
	{0x1FA90, 0x1FABD},
	{0x1FABF, 0x1FAC5},
	{0x1FACE, 0x1FADB},
	{0x1FAE0, 0x1FAE8},
	{0x1FAF0, 0x1FAF8},
	{0x20000, 0x2FFFD},
	{0x30000, 0x3FFFD},
}

// Cut keeps as much of s as fits in width columns.
func Cut(s string, width int) string {
	// Composed for the same reason Columns is, and for one more: cutting a
	// decomposed string by raw runes can separate a mark from the character it
	// belongs to, and CutRight then starts a line with an orphan that draws on
	// whatever precedes it (#1824).
	s = nfcfold.Compose(s)
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

// CutRight keeps as much of the end of s as fits in width columns. A path is
// identified by its tail — "…/消费者重平衡" says which project this is, where
// the first characters of a long prefix rarely do.
func CutRight(s string, width int) string {
	s = nfcfold.Compose(s)
	n := 0
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0; i-- {
		w := RuneColumns(runes[i])
		if n+w > width {
			return string(runes[i+1:])
		}
		n += w
	}
	return s
}
