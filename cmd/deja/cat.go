package main

import (
	"strconv"
	"strings"
)

// The mark is a 24x22 pixel sprite printed as half blocks: one cell carries two
// pixel rows, so twenty-two rows of detail cost eleven lines. Every feature sits
// on a whole pair of rows, because a feature split across a cell boundary makes
// that cell carry two colours and prints as a seam.
//
// Three colours, each one the xterm-256 palette holds exactly, so the banner and
// the SVG assets are the same colour rather than two approximations of one: 103
// for the coat, 208 for the nose, 234 for the eyes. The eyes are painted rather
// than left transparent — a hole takes the background, and on a light terminal
// that turns the eye near-white against the coat.
//
// It is drawn here rather than stored as ready-made escape sequences so that a
// mood is a few cells of difference instead of a second copy of the whole
// animal.
const (
	catCoat    = 103
	catAccent  = 208
	catFeature = 234
)

// catBody is everything that never changes: ears, head, chest, haunches, paws.
// '#' is coat, 'n' the nose, '.' is nothing; eyes and tail are laid over it.
var catBody = []string{
	"....#............#......",
	"...###..........###.....",
	"...####........####.....",
	"...#####......#####.....",
	"...################.....",
	"..##################....",
	"..##################....",
	"..##################....",
	"..##################....",
	"..##################....",
	"..##################....",
	"..########nn########....",
	"..##################....",
	"...################.....",
	"....##############......",
	".....############.......",
	".....############.......",
	".....############.......",
	".....############.......",
	"....##############......",
	"....##############......",
	".....####....####.......",
}

type cell struct{ row, col int }

// Eyes carry the whole expression: the mark has no mouth, because at this size
// a mouth reads as a dark blob stuck to the nose rather than as a curve.
var catEyes = map[string][]cell{
	"tall":   {{7, 5}, {7, 6}, {8, 5}, {8, 6}, {9, 5}, {9, 6}, {7, 15}, {7, 16}, {8, 15}, {8, 16}, {9, 15}, {9, 16}},
	"wide":   {{6, 5}, {6, 6}, {7, 5}, {7, 6}, {8, 5}, {8, 6}, {9, 5}, {9, 6}, {6, 15}, {6, 16}, {7, 15}, {7, 16}, {8, 15}, {8, 16}, {9, 15}, {9, 16}},
	"closed": {{9, 4}, {9, 5}, {9, 6}, {9, 7}, {9, 14}, {9, 15}, {9, 16}, {9, 17}},
	"low":    {{10, 5}, {10, 6}, {11, 5}, {11, 6}, {10, 15}, {10, 16}, {11, 15}, {11, 16}},
	"wink":   {{9, 4}, {9, 5}, {9, 6}, {9, 7}, {7, 15}, {7, 16}, {8, 15}, {8, 16}, {9, 15}, {9, 16}},
}

// Ears are the other half of the expression, and the only part of the outline
// a mood is allowed to change.
var catEars = map[string][]string{
	"up": nil,
	"flat": {
		"........................",
		"........................",
		".###............###.....",
		".#####........#####.....",
	},
	"flick": {
		"....#...................",
		"...###..................",
		"...####.........###.....",
		"...#####......#####.....",
	},
}

// The tail hugs the body and only the tip moves. An arcing tail steps one column
// per row and those diagonals print as loose shards; a vertical shank stays
// solid and still reads as a wag, because the tip is what changes. It sits two
// columns clear of the body: one column reads as a printing fault and none fuses
// the two into a single blob.
var catTails = map[string][]cell{
	"up":   {{20, 18}, {20, 19}, {19, 18}, {19, 19}, {18, 19}, {18, 20}, {17, 19}, {17, 20}, {16, 19}, {16, 20}, {15, 19}, {15, 20}, {14, 19}, {14, 20}},
	"mid":  {{20, 18}, {20, 19}, {19, 18}, {19, 19}, {18, 19}, {18, 20}, {17, 19}, {17, 20}, {16, 19}, {16, 20}, {15, 19}, {15, 20}, {14, 20}, {14, 21}},
	"curl": {{20, 18}, {20, 19}, {21, 19}, {21, 20}, {21, 21}, {20, 21}, {20, 22}},
	"down": {{20, 18}, {20, 19}, {21, 19}, {21, 20}, {21, 21}},
}

// catMood is what the cat is doing about the thing that just happened. Only the
// moments the CLI actually has are listed: the banner prints twice, and a mood
// with no moment would be decoration pretending to be a signal.
type catMood struct {
	eyes, ears, tail string
	coat             int
}

var (
	// ready is the pose the SVG assets are drawn in, so the banner and the
	// mark on a page are the same animal in the same position.
	moodReady     = catMood{eyes: "tall", ears: "up", tail: "up", coat: catCoat}
	moodSurprised = catMood{eyes: "wide", ears: "up", tail: "mid", coat: catCoat}
	moodAsleep    = catMood{eyes: "closed", ears: "up", tail: "curl", coat: catCoat}
	moodNothing   = catMood{eyes: "low", ears: "flat", tail: "down", coat: catCoat}
)

// catGrid lays a mood over the body and returns the finished pixel grid.
func catGrid(m catMood) [][]byte {
	g := make([][]byte, len(catBody))
	for i, row := range catBody {
		g[i] = []byte(row)
	}
	if ears := catEars[m.ears]; ears != nil {
		for i, row := range ears {
			g[i] = []byte(row)
		}
	}
	for _, c := range catTails[m.tail] {
		g[c.row][c.col] = '#'
	}
	for _, c := range catEyes[m.eyes] {
		g[c.row][c.col] = 'o'
	}
	return g
}

func catColour(ch byte, coat int) (int, bool) {
	switch ch {
	case '#':
		return coat, true
	case 'n':
		return catAccent, true
	case 'o':
		return catFeature, true
	}
	return 0, false
}

// renderCat prints the sprite as half blocks. A cell takes the colour of the
// upper pixel as its foreground and the lower one as its background, which is
// how two rows fit in one line without losing either.
func renderCat(m catMood) []string {
	g := catGrid(m)
	out := make([]string, 0, len(g)/2)
	for y := 0; y+1 < len(g); y += 2 {
		var b strings.Builder
		cur := -1
		for x := range g[y] {
			top, okTop := catColour(g[y][x], m.coat)
			bot, okBot := catColour(g[y+1][x], m.coat)
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
