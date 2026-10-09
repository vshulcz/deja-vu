package tui

import (
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// Style is how a run of text draws. A zero colour keeps what the cell already
// had, so text written over a filled surface keeps the surface.
type Style struct {
	FG, BG  Color
	Bold    bool
	Italic  bool
	Reverse bool
}

type cell struct {
	s  string // "" marks the right half of a wide rune
	fg Color
	bg Color
	b  bool
	it bool
	rv bool
}

// Canvas is one frame, cell by cell. Screens draw into it; the terminal
// compares it with the last frame and writes only the rows that changed.
type Canvas struct {
	W, H  int
	cells []cell
}

func NewCanvas(w, h int) *Canvas {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	c := &Canvas{W: w, H: h, cells: make([]cell, w*h)}
	for i := range c.cells {
		c.cells[i].s = " "
	}
	return c
}

func (c *Canvas) at(x, y int) *cell {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return nil
	}
	return &c.cells[y*c.W+x]
}

// Fill paints a rectangle with a background and clears its text.
func (c *Canvas) Fill(x, y, w, h int, bg Color) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			if p := c.at(xx, yy); p != nil {
				*p = cell{s: " ", bg: bg}
			}
		}
	}
}

// Put writes s at (x, y), stopping before column max, and returns the column
// after the last one written. It does not wrap.
func (c *Canvas) Put(x, y int, s string, st Style, max int) int {
	if max > c.W {
		max = c.W
	}
	prev := (*cell)(nil)
	for _, r := range nfcfold.Compose(s) {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			continue
		}
		if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
			// A mark nothing composed with rides on the cell before it.
			if prev != nil {
				prev.s += string(r)
			}
			continue
		}
		w := termwidth.RuneColumns(r)
		if x+w > max {
			break
		}
		prev = c.at(x, y)
		if prev != nil {
			prev.s = string(r)
			c.style(prev, st)
		}
		if w == 2 {
			if p := c.at(x+1, y); p != nil {
				p.s = ""
				c.style(p, st)
			}
		}
		x += w
	}
	return x
}

// PutClip is Put that ends in "…" when s does not fit before max.
func (c *Canvas) PutClip(x, y int, s string, st Style, max int) int {
	if termwidth.Columns(s) <= max-x {
		return c.Put(x, y, s, st, max)
	}
	if max-x < 1 {
		return x
	}
	return c.Put(x, y, termwidth.Cut(s, max-x-1)+"…", st, max)
}

func (c *Canvas) style(p *cell, st Style) {
	if st.FG != 0 {
		p.fg = st.FG
	}
	if st.BG != 0 {
		p.bg = st.BG
	}
	p.b, p.it, p.rv = st.Bold, st.Italic, st.Reverse
}

// Recolor sets every cell in the rectangle to one foreground and background,
// keeping the text: what a modal does to the screen behind it.
func (c *Canvas) Recolor(x, y, w, h int, fg, bg Color) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			if p := c.at(xx, yy); p != nil {
				p.fg, p.bg, p.b, p.it, p.rv = fg, bg, false, false, false
			}
		}
	}
}

// Text returns the plain text of a row, for tests.
func (c *Canvas) Text(y int) string {
	var b strings.Builder
	for x := 0; x < c.W; x++ {
		b.WriteString(c.cells[y*c.W+x].s)
	}
	return b.String()
}

// Lines renders the canvas as one string per row, each starting from a reset,
// so any row can be written on its own.
func (c *Canvas) Lines(m Mode) []string {
	out := make([]string, c.H)
	for y := 0; y < c.H; y++ {
		var b strings.Builder
		var last *cell
		for x := 0; x < c.W; x++ {
			p := &c.cells[y*c.W+x]
			if p.s == "" {
				continue
			}
			if last == nil || p.fg != last.fg || p.bg != last.bg || p.b != last.b || p.it != last.it || p.rv != last.rv {
				b.WriteString("\x1b[0")
				if p.b {
					b.WriteString(";1")
				}
				if p.it && m != ModeMono {
					b.WriteString(";3")
				}
				if p.rv {
					b.WriteString(";7")
				}
				sgrColor(&b, p.fg, false, m)
				sgrColor(&b, p.bg, true, m)
				b.WriteString("m")
				last = p
			}
			b.WriteString(p.s)
		}
		b.WriteString("\x1b[0m")
		out[y] = b.String()
	}
	return out
}
