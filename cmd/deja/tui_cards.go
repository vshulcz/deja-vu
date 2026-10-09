package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// A card leads with what the session concluded, because that is what the
// reader came back for; the question it started from goes in the meta line.
// Until the session has been read whole, the question stands in.
func (a *tuiApp) headline(s model.Session) (head string, concluded bool) {
	if d := a.details[sessionKey(s)]; d != nil && len(d.conclusions) > 0 {
		return d.conclusions[0], true
	}
	if t := strings.TrimSpace(s.Title); t != "" {
		return strings.Join(strings.Fields(t), " "), false
	}
	return "(no prompt recorded)", false
}

func (a *tuiApp) sectionLabel() (string, string) {
	if len(a.query) > 0 {
		agents := map[string]bool{}
		for _, r := range a.rows {
			agents[r.s.Harness] = true
		}
		label := tuiCount(len(a.rows), "session") + " across " + tuiCount(len(agents), "agent")
		switch {
		case a.widened:
			return label, "none in this project, showing all"
		case a.scope == scopeHere:
			return label, "this project"
		case a.scope == scopeKept:
			return label, "kept after deletion"
		}
		return label, "all projects"
	}
	switch a.scope {
	case scopeHere:
		return "Recent in this project", num(a.total)
	case scopeKept:
		return "Kept after their agent deleted them", num(a.total)
	}
	return "Recent", num(a.total)
}

func tuiCount(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return num(n) + " " + word + "s"
}

// snippet is the matched line a card quotes: the first that does not repeat
// its headline, or the question it started from when every one does.
func (a *tuiApp) snippet(r tuiRow) string {
	if len(r.snips) == 0 {
		return ""
	}
	head, _ := a.headline(r.s)
	for _, sn := range r.snips {
		if !sameLine(sn, head) {
			return sn
		}
	}
	if t := strings.Join(strings.Fields(r.s.Title), " "); t != "" && !sameLine(t, head) {
		return t
	}
	return ""
}

// cardHeight is a card's rows plus the gap after it. A search hit keeps its
// third row even when the quote turns out empty, so cards do not jump as
// their sessions finish loading.
func (a *tuiApp) cardHeight(r tuiRow) int {
	if len(r.snips) > 0 {
		return 4
	}
	return 3
}

func (a *tuiApp) drawCards(l layout) {
	p := a.p
	top := l.bodyTop
	if n, s := dejaVu(string(a.query), a.rows); n > 0 && l.bodyH > 16 {
		a.drawDejaVu(l, n, s)
		top += 4
	}
	label, note := a.sectionLabel()
	nx := p.Put(l.listX+1, top, strings.ToUpper(label), fgs(cMuted), l.listX+l.listW)
	p.PutClip(nx+2, top, note, fgs(cFaint), l.listX+l.listW)
	top += 2
	height := l.bodyTop + l.bodyH - top
	// Scroll so the selected card is whole on screen.
	y0 := 0
	selY, selH := 0, 0
	for i, r := range a.rows {
		if i == a.sel {
			selY, selH = y0, a.cardHeight(r)-1
		}
		y0 += a.cardHeight(r)
	}
	if selY < a.scroll {
		a.scroll = selY
	}
	if selY+selH > a.scroll+height {
		a.scroll = selY + selH - height
	}
	y := top - a.scroll
	a.firstShown = -1
	for i, r := range a.rows {
		h := a.cardHeight(r)
		if y+h > top && y < top+height {
			if a.firstShown < 0 && y >= top {
				a.firstShown = i
			}
			a.drawCard(l, i, r, y, top, top+height)
			idx := i
			a.addZone(l.listX, max(y, top), l.listX+l.listW, min(y+h-1, top+height), func() { a.clickCard(idx) })
			a.want(r.s)
		}
		y += h
	}
	a.firstShown = max(a.firstShown, 0)
}

// drawDejaVu is the banner over a search that was asked before: how many
// sessions opened with this question, and what the newest one concluded.
func (a *tuiApp) drawDejaVu(l layout, n int, s model.Session) {
	p := a.p
	x0, x1 := l.listX+1, l.listX+l.listW
	y := l.bodyTop
	p.Fill(x0, y, x1-x0, 3, cSurf2)
	x := p.Put(x0+1, y, "✦", fgs(cMark), x1)
	x = p.Put(x+1, y, "Déjà vu.", bold(cText), x1)
	p.PutClip(x+1, y, "Asked in "+num(n)+" sessions before. Newest answer, "+tuiAgo(s.Updated, a.now)+":", fgs(cSub), x1-1)
	head, concluded := a.headline(s)
	if !concluded {
		head = "reading…"
	}
	p.PutClip(x0+3, y+1, head, bold(cText), x1-1)
	bx := p.Put(x0+3, y+2, "↵", bold(cAcc), x1)
	bx = p.Put(bx+1, y+2, "open it   ", fgs(cSub), x1)
	bx = p.Put(bx, y+2, "o", bold(cAcc), x1)
	p.Put(bx+1, y+2, "hand it to an agent", fgs(cSub), x1)
	a.want(s)
}

func (a *tuiApp) drawCard(l layout, i int, r tuiRow, y, minY, maxY int) {
	p := a.p
	sel := i == a.sel
	x0, x1 := l.listX, l.listX+l.listW
	surf := cBase
	if sel {
		surf = cSurf
	}
	lines := a.cardHeight(r) - 1
	for k := 0; k < lines; k++ {
		yy := y + k
		if yy < minY || yy >= maxY {
			continue
		}
		p.Fill(x0+1, yy, l.listW-1, 1, surf)
		if sel {
			p.Put(x0, yy, "▌", fgs(agentColor(r.s.Harness)), x1)
			if p.mono {
				p.Put(x0, yy, ">", bold(cText), x1)
			}
		}
	}
	row := func(k int) (int, bool) { yy := y + k; return yy, yy >= minY && yy < maxY }
	head, concluded := a.headline(r.s)
	if yy, ok := row(0); ok {
		x := x0 + 2
		if a.keptIDs[sessionKey(r.s)] {
			x = p.Put(x, yy, "◆ ", fgs(cPeach), x1)
		}
		p.PutClip(x, yy, head, boldOn(cText, surf), x1-1)
	}
	if yy, ok := row(1); ok {
		x := p.Put(x0+2, yy, "●", fgs(agentColor(r.s.Harness)), x1)
		meta := agentName(r.s.Harness)
		if a.scope != scopeHere || a.widened {
			meta += " · " + tuiProject(r.s)
		}
		meta += " · " + tuiAgo(r.s.Updated, a.now)
		x = p.PutClip(x+1, yy, meta, fgs(cSub), x1-1)
		if concluded && len(r.snips) == 0 && strings.TrimSpace(r.s.Title) != "" {
			p.PutClip(x, yy, " · "+strings.Join(strings.Fields(r.s.Title), " "), fgs(cMuted), x1-1)
		}
	}
	if yy, ok := row(2); ok {
		if sn := a.snippet(r); sn != "" {
			x := p.Put(x0+4, yy, "“", fgs(cMuted), x1)
			x = p.putHL(x, yy, sn, queryTerms(string(a.query)), on(cSub, surf), x1-2)
			p.Put(x, yy, "”", fgs(cMuted), x1)
		}
	}
}

// tuiProject trims the path a project name sometimes is to its last two
// parts, which is how people name their repositories.
func tuiProject(s model.Session) string {
	p := displayProject(s)
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

func (a *tuiApp) drawPreview(l layout) {
	s, ok := a.selected()
	if !ok {
		return
	}
	p := a.p
	x, y, w, h := l.prevX, l.bodyTop, l.prevW, l.bodyH
	p.box(x, y, w, h, cSurf)
	ix, iw := x+2, w-4
	right := ix + iw
	d := a.details[sessionKey(s)]
	r := a.rows[a.sel]
	cy := y + 1
	cx := p.Put(ix, cy, "●", fgs(agentColor(s.Harness)), right)
	cx = p.Put(cx+1, cy, agentName(s.Harness), bold(cText), right)
	p.PutClip(cx+2, cy, tuiProject(s)+" · "+tuiAgo(s.Updated, a.now), fgs(cMuted), right)
	cy += 2
	bottom := y + h - 3 // the buttons' row
	room := func(n int) bool { return cy+n < bottom-1 }
	gone := d != nil && d.gone
	if gone && room(3) {
		p.Put(ix, cy, "◆ Kept after deletion", bold(cPeach), right)
		cy++
		for _, ln := range wrapLines(agentName(s.Harness)+" deleted its copy. deja kept this one, and R writes it back.", iw, 2) {
			p.Put(ix, cy, ln, fgs(cSub), right)
			cy++
		}
		cy++
	}
	section := func(label string, lines []string, st func(int, string)) {
		if len(lines) == 0 || !room(2) {
			return
		}
		p.Put(ix, cy, label, fgs(cMuted), right)
		cy++
		for i, ln := range lines {
			if !room(1) {
				break
			}
			st(i, ln)
			cy++
		}
		cy++
	}
	plain := func(c tui.Color) func(int, string) {
		return func(_ int, ln string) { p.Put(ix, cy, ln, fgs(c), right) }
	}
	section("ASKED", wrapLines(s.Title, iw, 3), plain(cText))
	if len(r.snips) > 0 {
		terms := queryTerms(string(a.query))
		section("MATCHED", wrapLines(r.snips[0], iw-2, 3), func(_ int, ln string) {
			p.putHL(ix, cy, ln, terms, fgs(cSub), right)
		})
	}
	switch {
	case d == nil:
		section("CONCLUDED", []string{"reading…"}, plain(cFaint))
	case len(d.conclusions) == 0:
		section("CONCLUDED", []string{"Nothing the session settled on."}, plain(cFaint))
	default:
		var lines []string
		for _, c := range d.conclusions {
			lines = append(lines, wrapLines(c, iw-2, 3)...)
		}
		if len(lines) > 6 {
			lines = lines[:6]
		}
		section("CONCLUDED", lines, func(i int, ln string) {
			p.Put(ix, cy, "▎", fgs(cAcc), right)
			st := fgs(cText)
			st.Bold = i < 3
			p.Put(ix+2, cy, ln, st, right)
		})
	}
	if len(s.Touched) > 0 && room(1) {
		lx := p.Put(ix, cy, "TOUCHED", fgs(cMuted), right)
		p.Put(lx+3, cy, termwidth.CutRight(s.Touched[0], right-lx-3), fgs(cText), right)
		cy++
	}
	if room(1) {
		size := ""
		if d != nil {
			size = tuiCount(len(d.full.Messages), "message")
		}
		if s.Words > 0 {
			if size != "" {
				size += " · "
			}
			size += tuiCount(s.Words, "word")
		}
		if size != "" {
			lx := p.Put(ix, cy, "SIZE", fgs(cMuted), right)
			p.Put(lx+6, cy, size, fgs(cSub), right)
		}
	}
	a.drawActions(ix, bottom, right, gone)
}

// drawActions is the button row: one filled primary, the rest quiet.
func (a *tuiApp) drawActions(x, y, right int, gone bool) {
	p := a.p
	type act struct {
		k, label string
		do       func()
	}
	acts := []act{{"↵", "Read", a.openReader}, {"r", "Resume", a.resumeSelected}, {"o", "Continue in…", a.openContinue}}
	if gone {
		acts = []act{{"R", "Put back", a.putBack}, {"↵", "Read", a.openReader}, {"o", "Continue in…", a.openContinue}}
	}
	for i, ac := range acts {
		x0 := x
		x = p.button(x, y, ac.k, ac.label, i == 0, right)
		a.addZone(x0, y, x, y+1, ac.do)
		x += 2
		if x >= right {
			break
		}
	}
}
