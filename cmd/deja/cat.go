package main

import (
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/mark"
)

// The sprite itself lives in internal/mark, because the SVG assets are generated
// from the same grid and a second copy here would let the two drift with nothing
// to catch it. What stays is the half-block rendering, which only the terminal
// needs.
type catMood = mark.Mood

var (
	moodReady     = mark.Ready
	moodSurprised = mark.Surprised
	moodAsleep    = mark.Asleep
	moodNothing   = mark.Nothing
)

// renderCat prints the sprite as half blocks. A cell takes the colour of the
// upper pixel as its foreground and the lower one as its background, which is
// how two rows fit in one line without losing either.
func renderCat(m catMood) []string {
	g := mark.Grid(m)
	out := make([]string, 0, len(g)/2)
	for y := 0; y+1 < len(g); y += 2 {
		var b strings.Builder
		cur := -1
		for x := range g[y] {
			top, okTop := mark.Colour(g[y][x], m.CoatColour)
			bot, okBot := mark.Colour(g[y+1][x], m.CoatColour)
			switch {
			case !okTop && !okBot:
				if cur != -1 {
					b.WriteString(logoReset)
					cur = -1
				}
				b.WriteByte(' ')
			case okTop && okBot && top == bot:
				if cur != top {
					b.WriteString(fgColour(top))
					cur = top
				}
				b.WriteString("█")
			case okTop && okBot:
				b.WriteString(fgColour(top) + bgColour(bot) + "▀" + logoReset)
				cur = -1
			case okTop:
				if cur != top {
					b.WriteString(fgColour(top))
					cur = top
				}
				b.WriteString("▀")
			default:
				if cur != bot {
					b.WriteString(fgColour(bot))
					cur = bot
				}
				b.WriteString("▄")
			}
		}
		out = append(out, b.String()+logoReset)
	}
	return out
}

func fgColour(n int) string { return "\x1b[38;5;" + strconv.Itoa(n) + "m" }
func bgColour(n int) string { return "\x1b[48;5;" + strconv.Itoa(n) + "m" }
