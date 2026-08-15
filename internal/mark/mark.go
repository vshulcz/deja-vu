// Package mark holds the cat sprite: the one description of the animal that the
// terminal banner and the SVG assets both draw from.
//
// It lives here rather than in cmd/deja because the assets were hand-authored
// from a throwaway script, which meant a change to the sprite would not reach
// them and nothing would say so. Now both readers take the same grid.
package mark

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
	"curl": {{20, 18}, {20, 19}, {21, 19}, {21, 20}, {21, 21}, {20, 21}, {20, 22}},
	"down": {{20, 18}, {20, 19}, {21, 19}, {21, 20}, {21, 21}},
}

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
