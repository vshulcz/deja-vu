// Package mark holds the cat sprite: the one description of the animal that the
// terminal banner and the SVG assets both draw from.
//
// It lives here rather than in cmd/deja because the assets were hand-authored
// from a throwaway script, which meant a change to the sprite would not reach
// them and nothing would say so. Now both readers take the same grid.
package mark

import (
	"fmt"
	"sort"
	"strings"
)

// The mark is a 24x22 pixel sprite. In the terminal it prints as half blocks:
// one cell carries two pixel rows, so twenty-two rows of detail cost eleven
// lines. Every feature sits on a whole pair of rows, because a feature split
// across a cell boundary makes that cell carry two colours and prints as a seam.
//
// Three colours, each one the xterm-256 palette holds exactly, so the banner and
// the SVG assets are the same colour rather than two approximations of one: 103
// for the coat, 208 for the nose, 234 for the eyes. The eyes are painted rather
// than left transparent — a hole takes the background, and on a light terminal
// that turns the eye near-white against the coat.
const (
	Coat    = 103
	Accent  = 208
	Feature = 234
)

// Hex gives each palette entry its exact sRGB value, so an SVG and a terminal
// that both honour the palette land on the same pixel.
var Hex = map[int]string{
	Coat:    "#8787af",
	Accent:  "#ff8700",
	Feature: "#1c1c1c",
}

// Body is everything that never changes: ears, head, chest, haunches, paws.
// '#' is coat, 'n' the nose, '.' is nothing; eyes and tail are laid over it.
var Body = []string{
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

// Cell is one pixel of the sprite, addressed row then column.
type Cell struct{ Row, Col int }

// Eyes carry the whole expression: the mark has no mouth, because at this size
// a mouth reads as a dark blob stuck to the nose rather than as a curve.
var Eyes = map[string][]Cell{
	// "none" is the face with no eyes painted at all. An animated mark needs it:
	// the still body underneath has to carry coat where the eyes will go, or
	// hiding the open-eye group leaves two holes in the head.
	"none":   nil,
	"tall":   {{7, 5}, {7, 6}, {8, 5}, {8, 6}, {9, 5}, {9, 6}, {7, 15}, {7, 16}, {8, 15}, {8, 16}, {9, 15}, {9, 16}},
	"wide":   {{6, 5}, {6, 6}, {7, 5}, {7, 6}, {8, 5}, {8, 6}, {9, 5}, {9, 6}, {6, 15}, {6, 16}, {7, 15}, {7, 16}, {8, 15}, {8, 16}, {9, 15}, {9, 16}},
	"closed": {{9, 4}, {9, 5}, {9, 6}, {9, 7}, {9, 14}, {9, 15}, {9, 16}, {9, 17}},
	"low":    {{10, 5}, {10, 6}, {11, 5}, {11, 6}, {10, 15}, {10, 16}, {11, 15}, {11, 16}},
	"wink":   {{9, 4}, {9, 5}, {9, 6}, {9, 7}, {7, 15}, {7, 16}, {8, 15}, {8, 16}, {9, 15}, {9, 16}},
}

// Ears are the other half of the expression, and the only part of the outline
// a mood is allowed to change.
var Ears = map[string][]string{
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
var Tails = map[string][]Cell{
	"up":   {{20, 18}, {20, 19}, {19, 18}, {19, 19}, {18, 19}, {18, 20}, {17, 19}, {17, 20}, {16, 19}, {16, 20}, {15, 19}, {15, 20}, {14, 19}, {14, 20}},
	"mid":  {{20, 18}, {20, 19}, {19, 18}, {19, 19}, {18, 19}, {18, 20}, {17, 19}, {17, 20}, {16, 19}, {16, 20}, {15, 19}, {15, 20}, {14, 20}, {14, 21}},
	"out":  {{20, 18}, {20, 19}, {19, 18}, {19, 19}, {18, 19}, {18, 20}, {17, 19}, {17, 20}, {16, 19}, {16, 20}, {15, 20}, {15, 21}, {14, 21}, {14, 22}},
	"curl": {{20, 18}, {20, 19}, {21, 19}, {21, 20}, {21, 21}, {20, 21}, {20, 22}},
	"down": {{20, 18}, {20, 19}, {21, 19}, {21, 20}, {21, 21}},
}

// WagCycle is the wag: three positions of the same cells, ping-ponged, so the
// tip travels out and comes back rather than snapping between two poses. Two
// poses alternating reads as a twitch — the middle one is what makes it a
// gesture. The body never moves, or the whole animal reads as jitter.
var WagCycle = []string{"up", "mid", "out", "mid"}

// Mood is what the cat is doing about the thing that just happened. Only the
// moments the CLI actually has are listed: the banner prints twice, and a mood
// with no moment would be decoration pretending to be a signal.
type Mood struct {
	EyeSet, EarSet, TailSet string
	CoatColour              int
}

var (
	// Ready is the pose the SVG assets are drawn in, so the banner and the
	// mark on a page are the same animal in the same position.
	Ready     = Mood{EyeSet: "tall", EarSet: "up", TailSet: "up", CoatColour: Coat}
	Surprised = Mood{EyeSet: "wide", EarSet: "up", TailSet: "mid", CoatColour: Coat}
	Asleep    = Mood{EyeSet: "closed", EarSet: "up", TailSet: "curl", CoatColour: Coat}
	Nothing   = Mood{EyeSet: "low", EarSet: "flat", TailSet: "down", CoatColour: Coat}
)

// Grid lays a mood over the body and returns the finished pixel grid.
func Grid(m Mood) [][]byte {
	g := make([][]byte, len(Body))
	for i, row := range Body {
		g[i] = []byte(row)
	}
	if ears := Ears[m.EarSet]; ears != nil {
		for i, row := range ears {
			g[i] = []byte(row)
		}
	}
	for _, c := range Tails[m.TailSet] {
		g[c.Row][c.Col] = '#'
	}
	for _, c := range Eyes[m.EyeSet] {
		g[c.Row][c.Col] = 'o'
	}
	return g
}

// Path is one SVG fill: every cell of a single colour, as path data.
type Path struct {
	Fill string
	D    string
}

// cellsPath writes cells as path data, merging horizontally adjacent ones into a
// single rectangle. One path per colour rather than one rect per pixel: at
// fractional scale a per-row fill leaves a hairline of background between rows,
// which is the seam the first hand-drawn assets had.
func cellsPath(cells []Cell, x0, y0, size int) string {
	byRow := map[int][]int{}
	rows := []int{}
	for _, c := range cells {
		if _, seen := byRow[c.Row]; !seen {
			rows = append(rows, c.Row)
		}
		byRow[c.Row] = append(byRow[c.Row], c.Col)
	}
	sort.Ints(rows)
	var b strings.Builder
	for _, r := range rows {
		cols := byRow[r]
		sort.Ints(cols)
		for i := 0; i < len(cols); {
			j := i
			for j+1 < len(cols) && cols[j+1] == cols[j]+1 {
				j++
			}
			w := (j - i + 1) * size
			fmt.Fprintf(&b, "M%d %dh%dv%dh-%dZ", x0+cols[i]*size, y0+r*size, w, size, w)
			i = j + 1
		}
	}
	return b.String()
}

// order keeps the colours in a fixed sequence: coat, features, nose. A map walk
// would reorder the paths on every run and turn a no-op regeneration into a diff.
var order = []int{Coat, Feature, Accent}

// Paths draws a mood as one path per colour, placed at x0,y0 with the given cell
// size. Callers that animate a part pass a skip so they can draw that part
// themselves.
func Paths(m Mood, x0, y0, size int, skip func(Cell) bool) []Path {
	g := Grid(m)
	byColour := map[int][]Cell{}
	for r, row := range g {
		for c := range row {
			col, ok := Colour(row[c], m.CoatColour)
			cell := Cell{Row: r, Col: c}
			if !ok || (skip != nil && skip(cell)) {
				continue
			}
			byColour[col] = append(byColour[col], cell)
		}
	}
	out := make([]Path, 0, len(order))
	for _, col := range order {
		if cells := byColour[col]; len(cells) > 0 {
			out = append(out, Path{Fill: Hex[col], D: cellsPath(cells, x0, y0, size)})
		}
	}
	return out
}

// CellsD is path data for a named set of cells — one wag position, one eye set —
// so an animated caller can put each frame in its own group.
func CellsD(cells []Cell, x0, y0, size int) string {
	return cellsPath(cells, x0, y0, size)
}

// Colour maps a grid character to its palette entry.
func Colour(ch byte, coat int) (int, bool) {
	switch ch {
	case '#':
		return coat, true
	case 'n':
		return Accent, true
	case 'o':
		return Feature, true
	}
	return 0, false
}
