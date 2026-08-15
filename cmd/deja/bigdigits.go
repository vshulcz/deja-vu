package main

import (
	"strings"
)

// Numbers drawn as pixels, so the card can have a hierarchy.
//
// A terminal has no type sizes. Colour and space are the only levers, and on a
// card where one figure is the whole point, neither is enough — the hero number
// printed at the same weight as its own caption is not a hero. Drawing it from
// the same pixel grid the mark uses gives it the size it needs and keeps it in
// the one visual language the project already has.
//
// Five rows of pixels, printed as half blocks, is three lines of text: big
// enough to lead the card, small enough to leave room for what explains it.

// Each glyph is five rows of three columns. Three is the narrowest a digit can
// be and stay unambiguous; the columns are doubled on the way out, because a
// one-column stroke against a two-column counter reads as a smear rather than
// as a number.
var digitPixels = map[rune][5]string{
	'0': {"###", "# #", "# #", "# #", "###"},
	'1': {" # ", "## ", " # ", " # ", "###"},
	'2': {"###", "  #", "###", "#  ", "###"},
	'3': {"###", "  #", "###", "  #", "###"},
	'4': {"# #", "# #", "###", "  #", "  #"},
	'5': {"###", "#  ", "###", "  #", "###"},
	'6': {"###", "#  ", "###", "# #", "###"},
	'7': {"###", "  #", "  #", "  #", "  #"},
	'8': {"###", "# #", "###", "# #", "###"},
	'9': {"###", "# #", "###", "  #", "###"},
	',': {"   ", "   ", "   ", " ##", " # "},
	'.': {"   ", "   ", "   ", "   ", " ##"},
	'k': {"   ", "# #", "## ", "# #", "# #"},
	'M': {"   ", "###", "###", "# #", "# #"},
	'+': {"   ", " # ", "###", " # ", "   "},
	' ': {"   ", "   ", "   ", "   ", "   "},
}

const digitGap = 1 // blank columns between glyphs, at the chosen scale

// bigNumber renders text as half blocks in one colour, as wide as it can be
// within max columns. Anything it has no glyph for is dropped rather than
// guessed at: a placeholder box in the middle of the headline figure is worse
// than a character short.
//
// The scale is chosen rather than fixed. A two-digit figure fits doubled and a
// five-digit one does not, and a fixed scale pushed the border eight columns
// past the frame the first time a card led with sessions instead of recalls.
func bigNumber(text string, colour int, max int) []string {
	glyphs := 0
	for _, r := range text {
		if _, ok := digitPixels[r]; ok {
			glyphs++
		}
	}
	if glyphs == 0 {
		return nil
	}
	scale := 2
	if glyphs*(3*scale+digitGap) > max {
		scale = 1
	}
	if glyphs*(3*scale+digitGap) > max {
		return []string{paint(colour, text)}
	}
	return drawBig(text, colour, scale)
}

func drawBig(text string, colour, digitScale int) []string {
	var rows [5]strings.Builder
	for _, r := range text {
		glyph, ok := digitPixels[r]
		if !ok {
			continue
		}
		for y := 0; y < 5; y++ {
			for _, px := range glyph[y] {
				fill := ' '
				if px == '#' {
					fill = '#'
				}
				rows[y].WriteString(strings.Repeat(string(fill), digitScale))
			}
			rows[y].WriteString(strings.Repeat(" ", digitGap))
		}
	}

	// Five rows is odd, so the last text line carries one pixel row over an
	// empty one — the same way the sprite's own last line works.
	var out []string
	for y := 0; y < 5; y += 2 {
		top := rows[y].String()
		bottom := ""
		if y+1 < 5 {
			bottom = rows[y+1].String()
		}
		out = append(out, halfBlockRow(top, bottom, colour))
	}
	return out
}

// halfBlockRow prints two pixel rows in one line of cells: the upper row as the
// foreground of ▀ and the lower as its background.
func halfBlockRow(top, bottom string, colour int) string {
	width := len(top)
	if len(bottom) > width {
		width = len(bottom)
	}
	at := func(s string, i int) bool { return i < len(s) && s[i] == '#' }

	var b strings.Builder
	for i := 0; i < width; i++ {
		upper, lower := at(top, i), at(bottom, i)
		switch {
		case upper && lower:
			b.WriteString(fgColour(colour) + "█" + logoReset)
		case upper:
			b.WriteString(fgColour(colour) + "▀" + logoReset)
		case lower:
			b.WriteString(fgColour(colour) + "▄" + logoReset)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}
