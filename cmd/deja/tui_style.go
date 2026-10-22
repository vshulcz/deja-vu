package main

import (
	"os"
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/harnesscolor"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The interactive screen's palette. Every colour is named for its job, so the
// light set replaces the values without touching layout.
var (
	cBase, cMantle, cSurf, cSurf2, cOver tui.Color
	cText, cSub, cMuted, cFaint          tui.Color
	cAcc, cYel, cGrn, cPeach, cRed       tui.Color
	// cMark is what a match is picked out in: yellow reads on dark and
	// vanishes on light.
	cMark tui.Color
)

func init() { setTheme("dark") }

// palettes holds each theme's colours in the order setTheme assigns them.
// dark and light are Catppuccin Mocha and Latte. dracula takes Dracula's
// colours and its editor ports' background shades, with comment for muted
// text; the card surface and faint text sit between them at Mocha's contrast,
// far enough apart that each keeps its own xterm-256 entry.
var palettes = map[string][]string{
	"dark": {"#1e1e2e", "#181825", "#26263a", "#313244", "#45475a", "#cdd6f4", "#a6adc8", "#7f849c", "#585b70",
		"#cba6f7", "#f9e2af", "#a6e3a1", "#fab387", "#f38ba8", "#f9e2af"},
	"light": {"#eff1f5", "#e6e9ef", "#e4e6ee", "#dce0e8", "#bcc0cc", "#4c4f69", "#5c5f77", "#7c7f93", "#9ca0b0",
		"#8839ef", "#df8e1d", "#40a02b", "#fe640b", "#d20f39", "#fe640b"},
	"dracula": {"#282a36", "#21222c", "#313342", "#3a3d4e", "#44475a", "#f8f8f2", "#bfc3d9", "#6272a4", "#5a5e7e",
		"#bd93f9", "#f1fa8c", "#50fa7b", "#ffb86c", "#ff5555", "#f1fa8c"},
}

// palettes256 pins the xterm-256 entry of a colour, by its place in the
// palette, where the nearest entry would merge it with a neighbour. Latte's
// surf and surf2 both round to 254, so the key chips lost their fill on the
// panels; surf2 takes the next grey down (#5107). Base and mantle both round
// to 255, and no grey sits between 255 and surf's 254, so the header bar and
// code blocks take white, a step lighter than the page instead of darker
// (#5355).
var palettes256 = map[string]map[int]int{
	"light": {1: 231, 3: 253},
}

// setTheme switches to the named palette; a name it does not know is dark.
func setTheme(name string) {
	p, ok := palettes[name]
	if !ok {
		name, p = "dark", palettes["dark"]
	}
	for i, c := range []*tui.Color{&cBase, &cMantle, &cSurf, &cSurf2, &cOver, &cText, &cSub, &cMuted, &cFaint,
		&cAcc, &cYel, &cGrn, &cPeach, &cRed, &cMark} {
		*c = tui.Hex(p[i])
		if n, ok := palettes256[name][i]; ok {
			*c = c.With256(n)
		}
	}
}

// terminalTheme reads the theme the reader asked for, then the background
// the terminal reports through COLORFGBG ("fg;bg", 7 and 15 being light).
// Unknown is dark, which is what most terminals are.
func terminalTheme() string {
	if name := strings.ToLower(os.Getenv("DEJA_THEME")); palettes[name] != nil {
		return name
	}
	parts := strings.Split(os.Getenv("COLORFGBG"), ";")
	if bg := parts[len(parts)-1]; bg == "7" || bg == "15" {
		return "light"
	}
	return "dark"
}

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
func (p painter) keycap(x, y int, k, label string, limit int) int {
	x = p.Put(x, y, " "+k+" ", tui.Style{FG: cText, BG: cSurf2, Bold: true, Reverse: p.mono}, limit)
	x = p.Put(x, y, " "+label, fgs(cSub), limit)
	return x + 3
}

// button is a quiet action; primary is the one filled with the accent, of
// which a panel has exactly one.
func (p painter) button(x, y int, k, label string, primary bool, limit int) int {
	if primary {
		return p.Put(x, y, " "+k+"  "+label+" ", tui.Style{FG: cMantle, BG: cAcc, Bold: true, Reverse: p.mono}, limit)
	}
	x = p.Put(x, y, " ", on(cText, cOver), limit)
	x = p.Put(x, y, k, boldOn(cAcc, cOver), limit)
	return p.Put(x, y, " "+label+" ", on(cText, cOver), limit)
}

// box draws a rounded border around a filled rectangle. The border cells
// keep what is under them: filled, their corners showed as a lighter frame
// outside the line.
func (p painter) box(x, y, w, h int, fill tui.Color) {
	if w < 2 || h < 2 {
		return
	}
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
func (p painter) chip(x, y int, h string, n int, sel bool, limit int) int {
	bg := cSurf2
	if sel {
		bg = cOver
	}
	x = p.Put(x, y, " ", on(cText, bg), limit)
	x = p.Put(x, y, "●", on(agentColor(h), bg), limit)
	x = p.Put(x, y, " "+agentName(h), on(cText, bg), limit)
	if n > 0 {
		x = p.Put(x, y, " "+grouped(n), on(cMuted, bg), limit)
	}
	return p.Put(x, y, " ", on(cText, bg), limit)
}

// putHL writes text with every occurrence of the query's words picked out.
// It clips with an ellipsis like PutClip.
// A first match past the edge brings its part of the line in from the left,
// after an ellipsis, so a hit is never drawn off screen.
func (p painter) putHL(x, y int, text string, terms []string, base tui.Style, limit int) int {
	if termwidth.Columns(text) > limit-x {
		if limit-x < 1 {
			return x
		}
		if from, to := firstMark(highlightMask([]rune(text), terms)); to > 0 {
			rs := []rune(text)
			if termwidth.Columns(string(rs[:to])) > limit-x-1 {
				// The match ends two thirds of the way in, its line's tail after it.
				lead := []rune(termwidth.CutRight(string(rs[:to]), (limit-x-2)*2/3))
				x = p.Put(x, y, "…", base, limit)
				text = string(rs[min(from, to-len(lead)):])
			}
		}
		if termwidth.Columns(text) > limit-x {
			text = strings.TrimRight(termwidth.Cut(text, limit-x-1), " ") + "…"
		}
	}
	rs := []rune(text)
	mark := highlightMask(rs, terms)
	hl := base
	hl.FG, hl.Bold = cMark, true
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
			x = p.Put(x, y, string(rs[start:i]), st, limit)
			start = i
		}
	}
	return x
}

// firstMark is the rune span of the first marked run, or 0, 0.
func firstMark(mark []bool) (int, int) {
	for i, m := range mark {
		if m {
			j := i
			for j < len(mark) && mark[j] {
				j++
			}
			return i, j
		}
	}
	return 0, 0
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
// without quotes or the one-letter noise. Each word once and at most 32 of
// them: every card's headline, line and ASKED scans for each, and a pasted
// log in the box would otherwise make one frame thousands of passes.
func queryTerms(q string) []string {
	var out []string
	seen := map[string]bool{}
	for f := range strings.FieldsSeq(strings.ReplaceAll(q, "\"", " ")) {
		k := strings.ToLower(f)
		if len([]rune(f)) < 2 || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, f)
		if len(out) == 32 {
			break
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
		lines[n-1] = strings.TrimRight(last, " ") + "…"
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
