package main

import (
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/harnesscolor"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The interactive screen's palette. Dark only for now; every colour is named
// for its job so a light set can replace the values without touching layout.
var (
	cBase   = tui.Hex("#1e1e2e")
	cMantle = tui.Hex("#181825")
	cSurf   = tui.Hex("#26263a")
	cSurf2  = tui.Hex("#313244")
	cOver   = tui.Hex("#45475a")
	cText   = tui.Hex("#cdd6f4")
	cSub    = tui.Hex("#a6adc8")
	cMuted  = tui.Hex("#7f849c")
	cFaint  = tui.Hex("#585b70")
	cAcc    = tui.Hex("#cba6f7")
	cYel    = tui.Hex("#f9e2af")
	cGrn    = tui.Hex("#a6e3a1")
	cPeach  = tui.Hex("#fab387")
)

func agentColor(h string) tui.Color { return tui.Index256(harnesscolor.Index(h)) }

func fgs(c tui.Color) tui.Style       { return tui.Style{FG: c} }
func bold(c tui.Color) tui.Style      { return tui.Style{FG: c, Bold: true} }
func on(f, b tui.Color) tui.Style     { return tui.Style{FG: f, BG: b} }
func boldOn(f, b tui.Color) tui.Style { return tui.Style{FG: f, BG: b, Bold: true} }

// painter wraps the canvas with the few shapes every screen repeats.
type painter struct {
	*tui.Canvas
	mono bool
}

// keycap draws " k " on a surface and the label after it.
func (p painter) keycap(x, y int, k, label string, max int) int {
	x = p.Put(x, y, " "+k+" ", tui.Style{FG: cText, BG: cSurf2, Bold: true, Reverse: p.mono}, max)
	x = p.Put(x, y, " "+label, fgs(cSub), max)
	return x + 3
}

// button is a quiet action; primary is the one filled with the accent, of
// which a panel has exactly one.
func (p painter) button(x, y int, k, label string, primary bool, max int) int {
	if primary {
		return p.Put(x, y, " "+k+"  "+label+" ", tui.Style{FG: cMantle, BG: cAcc, Bold: true, Reverse: p.mono}, max)
	}
	x = p.Put(x, y, " ", on(cText, cOver), max)
	x = p.Put(x, y, k, boldOn(cAcc, cOver), max)
	return p.Put(x, y, " "+label+" ", on(cText, cOver), max)
}

// box draws a rounded border around a filled rectangle. The border cells
// keep what is under them: filled, their corners showed as a lighter frame
// outside the line.
func (p painter) box(x, y, w, h int, fill tui.Color) {
	p.Fill(x+1, y+1, w-2, h-2, fill)
	st := fgs(cOver)
	p.Put(x, y, "╭"+strings.Repeat("─", w-2)+"╮", st, x+w)
	for yy := y + 1; yy < y+h-1; yy++ {
		p.Put(x, yy, "│", st, x+w)
		p.Put(x+w-1, yy, "│", st, x+w)
	}
	p.Put(x, y+h-1, "╰"+strings.Repeat("─", w-2)+"╯", st, x+w)
}

// chip is "● Name count" on a surface.
func (p painter) chip(x, y int, h string, n int, sel bool, max int) int {
	bg := cSurf2
	if sel {
		bg = cOver
	}
	x = p.Put(x, y, " ", on(cText, bg), max)
	x = p.Put(x, y, "●", on(agentColor(h), bg), max)
	x = p.Put(x, y, " "+agentName(h), on(cText, bg), max)
	if n > 0 {
		x = p.Put(x, y, " "+num(n), on(cMuted, bg), max)
	}
	return p.Put(x, y, " ", on(cText, bg), max)
}

// putHL writes text with every occurrence of the query's words picked out.
// It clips with an ellipsis like PutClip.
func (p painter) putHL(x, y int, text string, terms []string, base tui.Style, max int) int {
	if termwidth.Columns(text) > max-x {
		if max-x < 1 {
			return x
		}
		text = termwidth.Cut(text, max-x-1) + "…"
	}
	rs := []rune(text)
	mark := highlightMask(rs, terms)
	hl := base
	hl.FG, hl.Bold = cYel, true
	if p.mono {
		hl.Reverse = true
	}
	start := 0
	for i := 1; i <= len(rs); i++ {
		if i == len(rs) || mark[i] != mark[start] {
			st := base
			if mark[start] {
				st = hl
			}
			x = p.Put(x, y, string(rs[start:i]), st, max)
			start = i
		}
	}
	return x
}

// highlightMask marks the runes of rs that belong to a case-insensitive match
// of any term. Rune-wise lowering keeps the indexes aligned.
func highlightMask(rs []rune, terms []string) []bool {
	mark := make([]bool, len(rs))
	low := make([]rune, len(rs))
	for i, r := range rs {
		low[i] = unicode.ToLower(r)
	}
	for _, t := range terms {
		tr := []rune(strings.ToLower(t))
		if len(tr) == 0 {
			continue
		}
		for i := 0; i+len(tr) <= len(low); i++ {
			match := true
			for j, r := range tr {
				if low[i+j] != r {
					match = false
					break
				}
			}
			if match {
				for j := range tr {
					mark[i+j] = true
				}
			}
		}
	}
	return mark
}

// queryTerms is what the highlighter looks for: the words of the query,
// without quotes or the one-letter noise.
func queryTerms(q string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ReplaceAll(q, "\"", " ")) {
		if len([]rune(f)) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

// wrapLines wraps text to width and keeps at most n lines, the last ending in
// an ellipsis when something was cut.
func wrapLines(text string, width, n int) []string {
	text = strings.Join(strings.Fields(text), " ")
	if width < 4 || text == "" {
		return nil
	}
	lines := termwidth.Wrap(text, width)
	if len(lines) > n {
		lines = lines[:n]
		last := lines[n-1]
		if termwidth.Columns(last) >= width {
			last = termwidth.Cut(last, width-1)
		}
		lines[n-1] = last + "…"
	}
	return lines
}

func num(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
