// Package tui is the terminal layer behind deja's interactive screen: raw
// mode, input decoding, and a cell canvas that redraws only what changed. The
// module has no dependencies, so this is the stdlib-only version of what a
// TUI library would provide, cut down to what one screen needs.
package tui

import (
	"os"
	"strconv"
	"strings"
)

// Color is 0 for "keep what is underneath" and otherwise 1<<24 | 0xRRGGBB.
type Color uint32

const colorSet = 1 << 24

// Hex parses "#rrggbb". Anything else is 0, which draws as nothing.
func Hex(s string) Color {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0
	}
	return Color(v) | colorSet
}

func (c Color) rgb() (r, g, b int) {
	return int(c>>16) & 0xff, int(c>>8) & 0xff, int(c) & 0xff
}

// Mode is how many colours the terminal can show.
type Mode int

const (
	ModeTrue Mode = iota
	Mode256
	ModeMono
)

// DetectMode reads the environment the way most terminal programs do.
// NO_COLOR wins. COLORTERM is the only reliable truecolor signal, plus the two
// terminals that support it without setting it; everything else gets the 256
// palette, which every terminal in use today draws.
func DetectMode() Mode {
	if os.Getenv("NO_COLOR") != "" {
		return ModeMono
	}
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return ModeTrue
	}
	if os.Getenv("WT_SESSION") != "" || os.Getenv("TERM_PROGRAM") == "iTerm.app" {
		return ModeTrue
	}
	return Mode256
}

var cubeLevels = [6]int{0, 95, 135, 175, 215, 255}

// to256 picks the nearest xterm-256 entry: the 6x6x6 cube or the grey ramp,
// whichever lands closer.
func to256(c Color) int {
	r, g, b := c.rgb()
	near := func(v int) int {
		best, bi := 1<<30, 0
		for i, l := range cubeLevels {
			if d := (v - l) * (v - l); d < best {
				best, bi = d, i
			}
		}
		return bi
	}
	ri, gi, bi := near(r), near(g), near(b)
	cube := 16 + 36*ri + 6*gi + bi
	cr, cg, cb := cubeLevels[ri], cubeLevels[gi], cubeLevels[bi]
	avg := (r + g + b) / 3
	gi2 := (avg - 3) / 10 // the nearest step of 8, 18, ... 238
	if gi2 < 0 {
		gi2 = 0
	}
	if gi2 > 23 {
		gi2 = 23
	}
	gv := 8 + gi2*10
	dist := func(x, y, z int) int { return (r-x)*(r-x) + (g-y)*(g-y) + (b-z)*(b-z) }
	if dist(gv, gv, gv) < dist(cr, cg, cb) {
		return 232 + gi2
	}
	return cube
}

// Index256 turns an xterm-256 index back into a Color, so agent colours kept
// as palette numbers draw on the same canvas as everything else.
func Index256(n int) Color {
	if n < 16 || n > 255 {
		return Hex("#bcbcbc")
	}
	if n >= 232 {
		v := 8 + (n-232)*10
		return Color(v<<16|v<<8|v) | colorSet
	}
	i := n - 16
	r, g, b := cubeLevels[i/36], cubeLevels[(i/6)%6], cubeLevels[i%6]
	return Color(r<<16|g<<8|b) | colorSet
}

func sgrColor(b *strings.Builder, c Color, bg bool, m Mode) {
	if c == 0 || m == ModeMono {
		return
	}
	base := "38"
	if bg {
		base = "48"
	}
	if m == ModeTrue {
		r, g, bl := c.rgb()
		b.WriteString(";" + base + ";2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(bl))
		return
	}
	b.WriteString(";" + base + ";5;" + strconv.Itoa(to256(c)))
}
