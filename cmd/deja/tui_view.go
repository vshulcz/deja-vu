package main

import (
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/mark"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// layout is where the parts of the list screen go at the current size.
type layout struct {
	w, h           int
	bodyTop, bodyH int
	listX, listW   int
	prevX, prevW   int // prevW 0: no preview at this width
}

func newLayout(w, h int) layout {
	l := layout{w: w, h: h, bodyTop: 6}
	if h < 18 {
		l.bodyTop = 4
	}
	l.bodyH = h - 1 - l.bodyTop
	l.listX = 1
	if w >= 96 {
		l.listW = w * 55 / 100
		if l.listW > 74 {
			l.listW = 74
		}
		l.prevX = l.listX + l.listW + 2
		l.prevW = w - l.prevX - 1
	} else {
		l.listW = w - 2
	}
	return l
}

func (a *tuiApp) render() {
	a.t.Draw(a.frame(a.t.Size()))
}

// frame draws the whole screen at one size. It reads the app's state and
// nothing else, so a test can draw a frame without a terminal.
func (a *tuiApp) frame(w, h int) *tui.Canvas {
	c := tui.NewCanvas(w, h)
	a.p.Canvas = c
	a.now = time.Now()
	if a.clock != nil {
		a.now = a.clock()
	}
	a.zones = a.zones[:0]
	c.Fill(0, 0, w, h, cBase)
	switch a.view {
	case viewReader:
		a.drawReader()
	case viewWelcome:
		a.drawWelcome()
	default:
		a.drawList()
	}
	if a.modal != modalNone {
		a.drawModal()
	}
	a.drawToast()
	return c
}

func (a *tuiApp) drawList() {
	l := newLayout(a.p.W, a.p.H)
	a.drawTop(l)
	a.drawSearch(l, 2)
	if l.bodyTop == 6 && len(a.rows) > 0 {
		a.drawStrip(l, 4)
	}
	if len(a.rows) == 0 {
		a.drawEmpty(l)
	} else {
		a.drawCards(l)
		if l.prevW > 0 {
			a.drawPreview(l)
		}
	}
	a.drawFooter()
}

func (a *tuiApp) drawTop(l layout) {
	p := a.p
	p.Fill(0, 0, l.w, 1, cMantle)
	x := p.Put(1, 0, "◆ deja", boldOn(cAcc, cMantle), l.w)
	x += 3
	kept := "Kept"
	if a.keptLoaded && len(a.kept) > 0 {
		kept += " " + num(len(a.kept))
	}
	for i, name := range []string{"This project", "All projects", kept} {
		st := on(cSub, cMantle)
		if i == a.scope {
			st = tui.Style{FG: cMantle, BG: cAcc, Bold: true, Reverse: p.mono}
		}
		x0 := x
		x = p.Put(x, 0, " "+name+" ", st, l.w) + 1
		scope := i
		a.addZone(x0, 0, x, 1, func() { a.setScope(scope) })
	}
	right := a.status()
	rw := termwidth.Columns(right)
	if l.w-rw-2 > x+2 {
		p.Put(l.w-rw-1, 0, right, on(cMuted, cMantle), l.w)
	}
}

func (a *tuiApp) status() string {
	switch {
	case a.indexing:
		// Turns with the 250 ms tick, so a long read does not look stuck.
		spin := []string{"◐", "◓", "◑", "◒"}[int(a.now.UnixMilli()/250)%4]
		return spin + " reading new sessions…"
	case len(a.query) > 0 && !a.searching:
		return "searched " + num(len(a.allMeta)) + " sessions in " + formatMS(a.tookMS)
	}
	return num(len(a.allMeta)) + " sessions · " + num(len(a.agentsAll)) + " agents"
}

func formatMS(ms float64) string {
	if ms < 10 {
		return num(int(ms*10)/10) + "." + num(int(ms*10)%10) + " ms"
	}
	return num(int(ms)) + " ms"
}

func (a *tuiApp) drawSearch(l layout, y int) {
	p := a.p
	x1 := l.w - 2
	p.Fill(2, y, x1-2, 1, cSurf)
	p.Put(3, y, "⌕", fgs(cAcc), x1)
	count := ""
	switch {
	case len(a.query) == 0:
	case a.searching:
		count = "…"
	default:
		count = num(len(a.rows))
	}
	max := x1 - termwidth.Columns(count) - 2
	x := 6
	if len(a.query) == 0 {
		if !a.listFocus {
			x = p.Put(x, y, "▏", fgs(cAcc), max)
		}
		p.PutClip(x, y, "Search everything your agents ever did", fgs(cMuted), max)
	} else {
		q := string(a.query)
		if termwidth.Columns(q) > max-x-1 {
			q = "…" + termwidth.CutRight(q, max-x-2)
		}
		x = p.Put(x, y, q, bold(cText), max)
		if !a.listFocus {
			p.Put(x, y, "▏", fgs(cAcc), max+1)
		}
	}
	p.Put(x1-termwidth.Columns(count)-1, y, count, fgs(cMuted), x1)
}

func (a *tuiApp) drawStrip(l layout, y int) {
	p := a.p
	counts := a.agentsAll
	if len(a.query) > 0 || a.scope == scopeKept {
		ss := make([]model.Session, len(a.rows))
		for i, r := range a.rows {
			ss[i] = r.s
		}
		counts = tuiAgentCounts(ss)
	}
	x := 2
	tail := "a  filter agents"
	if len(a.filter) > 0 {
		tail = "a  filtered · change"
	}
	limit := l.w - termwidth.Columns(tail) - 12
	shown := 0
	for _, ac := range counts {
		label := 4 + termwidth.Columns(agentName(ac.h)) + len(num(ac.n))
		if x+label > limit {
			break
		}
		x = p.chip(x, y, ac.h, ac.n, a.filter[ac.h], l.w) + 1
		shown++
	}
	if more := len(counts) - shown; more > 0 {
		x = p.Put(x+1, y, "+"+num(more)+" more", fgs(cMuted), l.w)
	}
	x = p.Put(x+2, y, "a", bold(cAcc), l.w)
	p.Put(x+1, y, strings.TrimPrefix(tail, "a "), fgs(cMuted), l.w)
	a.addZone(2, y, l.w, y+1, func() { a.openModal(modalAgents) })
}

func (a *tuiApp) drawFooter() {
	p := a.p
	y := p.H - 1
	p.Fill(0, y, p.W, 1, cMantle)
	var keys [][2]string
	switch {
	case a.view == viewReader:
		keys = [][2]string{{"↑↓", "scroll"}, {"n N", "next hit"}, {"t", "full messages"}, {"r", "resume"}, {"o", "continue in…"}, {"c", "copy"}, {"esc", "back"}}
	case a.listFocus:
		keys = [][2]string{{"↵", "read"}, {"r", "resume"}, {"o", "continue in…"}, {"c", "copy context"}, {"a", "agents"}, {"/", "search"}, {"?", "help"}}
	case len(a.query) > 0:
		keys = [][2]string{{"↑↓", "pick"}, {"↵", "read"}, {"tab", "scope"}, {"^o", "continue in…"}, {"^k", "commands"}, {"esc", "clear"}}
	default:
		keys = [][2]string{{"type", "search"}, {"↑↓", "pick"}, {"↵", "read"}, {"tab", "scope"}, {"^k", "commands"}, {"?", "help"}, {"esc", "quit"}}
	}
	x := 2
	for _, k := range keys {
		need := termwidth.Columns(k[0]) + termwidth.Columns(k[1]) + 6
		if x+need > p.W {
			break
		}
		x = p.keycap(x, y, k[0], k[1], p.W)
	}
}

func (a *tuiApp) drawToast() {
	if a.toast == "" || time.Now().After(a.toastUntil) {
		a.toast = ""
		return
	}
	p := a.p
	mark, mc := "✓", cGrn
	if !a.toastGood {
		mark, mc = "!", cPeach
	}
	text := a.toast
	w := termwidth.Columns(text) + 6
	if w > p.W-4 {
		w = p.W - 4
	}
	x, y := p.W-w-2, p.H-3
	p.Fill(x, y, w, 1, cSurf2)
	p.Put(x+1, y, mark, boldOn(mc, cSurf2), x+w)
	p.PutClip(x+3, y, text, on(cText, cSurf2), x+w-1)
}

// drawCat paints the mascot in half blocks: one cell holds two pixel rows,
// the upper as foreground and the lower as background.
func (a *tuiApp) drawCat(x, y int, m mark.Mood) (w, h int) {
	g := mark.Grid(m)
	for row := 0; row+1 < len(g); row += 2 {
		for col := range g[row] {
			top, okT := mark.Colour(g[row][col], m.CoatColour)
			bot, okB := mark.Colour(g[row+1][col], m.CoatColour)
			cx, cy := x+col, y+row/2
			switch {
			case okT && okB:
				a.p.Put(cx, cy, "▀", on(tui.Index256(top), tui.Index256(bot)), a.p.W)
			case okT:
				a.p.Put(cx, cy, "▀", fgs(tui.Index256(top)), a.p.W)
			case okB:
				a.p.Put(cx, cy, "▄", fgs(tui.Index256(bot)), a.p.W)
			}
		}
		if len(g[row]) > w {
			w = len(g[row])
		}
	}
	return w, len(g) / 2
}

func (a *tuiApp) drawEmpty(l layout) {
	p := a.p
	mood := mark.Nothing
	title, line := "Nothing for “"+string(a.query)+"”", "Not in "+num(len(a.allMeta))+" sessions from "+num(len(a.agentsAll))+" agents on this machine."
	switch {
	case a.searching:
		return
	case len(a.query) == 0 && a.scope == scopeKept:
		mood = mark.Ready
		title, line = "Nothing deleted yet", "When an agent cleans up its old sessions, deja keeps them here."
		if !a.keptLoaded {
			title, line = "Checking…", "Looking for sessions their agents deleted."
		}
	case len(a.query) == 0:
		mood = mark.Asleep
		title, line = "No sessions here yet", "tab shows every project."
	}
	cw, ch := 24, 11
	x := (l.w - cw - 6 - 50) / 2
	if x < 2 {
		x = 2
	}
	y := l.bodyTop + (l.bodyH-ch)/2 - 1
	if y < l.bodyTop {
		y = l.bodyTop
	}
	tx := x + cw + 6
	if l.w >= 80 {
		a.drawCat(x, y, mood)
	} else {
		tx = 3
	}
	ty := y + 3
	p.PutClip(tx, ty, title, bold(cText), l.w-2)
	p.PutClip(tx, ty+2, line, fgs(cSub), l.w-2)
	if len(a.query) > 0 {
		bx := tx
		if a.scope == scopeHere {
			bx = p.button(bx, ty+4, "tab", "All projects", false, l.w-2) + 2
		}
		p.button(bx, ty+4, "^w", "Drop a word", false, l.w-2)
	}
}
