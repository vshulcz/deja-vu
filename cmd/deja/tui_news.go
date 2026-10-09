package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/tui"
)

// The first time the screen opens on a new version it says what the version
// brings, in the screen rather than on stderr after it closes: there it
// landed under whatever the screen handed over to, an agent session included.

// loadNews takes the what-changed list for this run, which also marks the
// version as seen so the plain-text notice stays quiet.
func (a *tuiApp) loadNews() {
	items, ver := whatChangedFor(a.dir)
	if len(items) == 0 {
		return
	}
	a.news, a.newsVer = items, ver
	a.modal = modalNews
}

// drawLeaving is the last frame before the screen hands the terminal to an
// agent: where the reader is going, with the session they picked.
func (a *tuiApp) drawLeaving() {
	p := a.p
	s, _ := a.current()
	x, y := max(2, (p.W-64)/2), max(1, (p.H-11)/2)
	tx := x
	if p.W >= 70 {
		a.drawCat(x, y, wagFrame(a.now))
		tx = x + 30
	}
	right := p.W - 2
	cx := p.Put(tx, y+3, "↗ ", fgs(cAcc), right)
	p.PutClip(cx, y+3, a.leaving, bold(cText), right)
	if s.ID != "" {
		head := a.headline(s)
		p.PutClip(tx, y+5, head, fgs(cSub), right)
		p.PutClip(tx, y+6, agentName(s.Harness)+" · "+tuiProject(s)+" · "+tuiAgo(s.Updated, a.now), fgs(cMuted), right)
	}
}

func (a *tuiApp) handleNews(ev tui.Event) {
	if ev.Kind == tui.EvKey {
		a.modal = modalNone
	}
}

func (a *tuiApp) drawNews() {
	p := a.p
	w := min(84, p.W-4)
	tw := w - 4 - 26
	if p.W < 70 {
		tw = w - 4
	}
	var lines [][]string
	h := 7
	for _, it := range a.news {
		l := wrapLines(strings.ReplaceAll(it, "`", ""), max(10, tw-2), 3)
		lines = append(lines, l)
		h += len(l) + 1
	}
	x, y, iw := a.modalBox(w, max(h, 14))
	right := x + iw
	tx := x
	if p.W >= 70 {
		a.drawCat(x, y+1, wagFrame(a.now))
		tx = x + 26
	}
	cx := p.Put(tx, y+1, "New in deja "+a.newsVer, bold(cText), right)
	p.Put(cx+2, y+1, "✦", fgs(cAcc), right)
	ly := y + 3
	for _, l := range lines {
		for i, s := range l {
			if i == 0 {
				p.Put(tx, ly, "•", fgs(cAcc), right)
			}
			p.PutClip(tx+2, ly, s, fgs(cText), right)
			ly++
		}
		ly++
	}
	p.Put(tx, ly, "any key to go on", fgs(cMuted), right)
}
