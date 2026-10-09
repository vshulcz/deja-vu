package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

type modalState struct {
	sel     int
	cols    int // the continue grid's, as last drawn
	filter  []rune
	src     model.Session
	targets []continueTarget
	picked  map[string]bool
	ids     []string
}

func (a *tuiApp) openModal(m int) {
	a.modal = m
	a.m = modalState{}
	if m == modalAgents {
		a.m.picked = map[string]bool{}
		for h := range a.filter {
			a.m.picked[h] = true
		}
		for _, ac := range a.agentsAll {
			a.m.ids = append(a.m.ids, ac.h)
		}
	}
}

func (a *tuiApp) handleModal(ev tui.Event) {
	if ev.Kind == tui.EvPaste && (a.modal == modalContinue || a.modal == modalPalette) {
		a.m.filter = append(a.m.filter, []rune(strings.Join(strings.Fields(ev.Text), " "))...)
		a.m.sel = 0
		return
	}
	if ev.Kind != tui.EvKey {
		return
	}
	if a.modal == modalPalette {
		a.handlePalette(ev)
		return
	}
	if ev.Key == tui.KeyEsc || (ev.Key == tui.KeyRune && ev.Rune == 'q' && a.modal == modalHelp) {
		a.modal = modalNone
		return
	}
	if a.modal == modalHelp {
		a.modal = modalNone
		return
	}
	n := len(a.m.ids)
	if a.modal == modalContinue {
		n = len(a.shownTargets())
	}
	switch ev.Key {
	case tui.KeyLeft:
		a.m.sel--
	case tui.KeyRight, tui.KeyTab:
		a.m.sel++
	case tui.KeyUp:
		a.m.sel = a.modalMove(-1)
	case tui.KeyDown:
		a.m.sel = a.modalMove(1)
	case tui.KeyEnter:
		a.modalEnter()
		return
	case tui.KeyBackspace:
		if k := len(a.m.filter); k > 0 && a.modal == modalContinue {
			a.m.filter = a.m.filter[:k-1]
			a.m.sel = 0
		}
	case tui.KeyRune:
		if a.modal == modalAgents {
			if ev.Rune == ' ' && a.m.sel < n {
				h := a.m.ids[a.m.sel]
				a.m.picked[h] = !a.m.picked[h]
				if !a.m.picked[h] {
					delete(a.m.picked, h)
				}
			}
			if ev.Rune == 'x' {
				a.m.picked = map[string]bool{}
			}
			return
		}
		a.m.filter = append(a.m.filter, ev.Rune)
		a.m.sel = 0
	}
	if a.m.sel >= n {
		a.m.sel = n - 1
	}
	if a.m.sel < 0 {
		a.m.sel = 0
	}
}

func (a *tuiApp) modalEnter() {
	switch a.modal {
	case modalAgents:
		a.filter = a.m.picked
		a.modal = modalNone
		a.reload()
	case modalContinue:
		ts := a.shownTargets()
		if a.m.sel >= len(ts) {
			return
		}
		a.continueIn(ts[a.m.sel])
	}
}

// continueIn hands the session to another agent. An agent with a command on
// this machine is started with the context as its first prompt; any other is
// given the context on the clipboard, which is all a paste-only app takes.
func (a *tuiApp) continueIn(t continueTarget) {
	s := a.m.src
	a.modal = modalNone
	if t.paste || !t.installed {
		a.copySession(s, "paste it into "+agentName(t.id))
		return
	}
	a.after = func() error {
		fmt.Fprintf(os.Stderr, "deja: continuing in %s\n", agentName(t.id))
		indexInHand = true
		return runHandoff(a.dir, []string{"--to", t.id, s.ID, "--exec"}, os.Stdout)
	}
	a.quit = true
}

func (a *tuiApp) copyContext() {
	a.remember()
	s, ok := a.current()
	if !ok {
		return
	}
	a.copySession(s, "paste it into any agent")
}

func (a *tuiApp) copySession(s model.Session, hint string) {
	d := a.details[sessionKey(s)]
	if d == nil {
		a.want(s)
		a.say("Still reading that session, try again in a moment.", false)
		return
	}
	text := handoffPrompt(digestHandoff(d.full))
	copyText := func(s string) error { return tuiCopy(a.t.Write, s) }
	if a.copy != nil {
		copyText = a.copy
	}
	if err := copyText(text); err != nil {
		a.say("Could not copy: "+err.Error(), false)
		return
	}
	a.say("Context copied · "+kb(len(text))+" · "+hint, true)
}

func kb(n int) string {
	if n < 1024 {
		return num(n) + " B"
	}
	return num(n/1024) + "." + num(n%1024*10/1024) + " KB"
}

func (a *tuiApp) drawModal() {
	p := a.p
	p.Recolor(0, 0, p.W, p.H, cFaint, cMantle)
	a.zones = a.zones[:0]
	switch a.modal {
	case modalContinue:
		a.drawContinue()
	case modalAgents:
		a.drawAgents()
	case modalHelp:
		a.drawHelp()
	case modalPalette:
		a.drawPalette()
	}
}

// modalBox places a centred box and returns its inner left edge and width.
func (a *tuiApp) modalBox(w, h int) (x, y, iw int) {
	p := a.p
	if w > p.W-4 {
		w = p.W - 4
	}
	if h > p.H-2 {
		h = p.H - 2
	}
	x, y = (p.W-w)/2, (p.H-h)/2
	if y > 3 {
		y = 3
	}
	p.box(x, y, w, h, cSurf)
	return x + 2, y + 1, w - 4
}

func (a *tuiApp) gridEntry(x, y, w int, h string, sel, dim bool, extra string) {
	p := a.p
	bg := cSurf
	if sel {
		bg = cOver
		p.Fill(x, y, w-1, 1, bg)
	}
	st := on(cText, bg)
	if dim {
		st = on(cMuted, bg)
	}
	cx := p.Put(x+1, y, "●", on(agentColor(h), bg), x+w)
	ex := x + w - 2 - termwidth.Columns(extra)
	nameMax := x + w - 2
	if extra != "" {
		nameMax = ex - 1
		p.Put(ex, y, extra, on(cFaint, bg), x+w)
	}
	p.PutClip(cx+1, y, agentName(h), st, nameMax)
}

func (a *tuiApp) drawAgents() {
	p := a.p
	rows := (len(a.m.ids) + 2) / 3
	x, y, iw := a.modalBox(96, rows+8)
	right := x + iw
	cx := p.Put(x, y, "Agents", bold(cText), right)
	p.PutClip(cx+3, y, num(len(a.agentsAll))+" with sessions here · "+num(len(agentNames))+" supported · space picks, none picked shows all", fgs(cMuted), right)
	y += 2
	colW := iw / 3
	counts := map[string]int{}
	for _, ac := range a.agentsAll {
		counts[ac.h] = ac.n
	}
	for i, h := range a.m.ids {
		r, c := i/3, i%3
		if y+r >= p.H-4 {
			break
		}
		box := "□ "
		if a.m.picked[h] {
			box = "■ "
		}
		ex := x + c*colW
		bg := cSurf
		if i == a.m.sel {
			bg = cOver
			p.Fill(ex, y+r, colW-1, 1, bg)
		}
		bx := p.Put(ex+1, y+r, box, on(cGrn, bg), ex+colW)
		a.gridEntry(bx-1, y+r, colW-(bx-1-ex), h, i == a.m.sel, false, num(counts[h]))
	}
	y += rows + 1
	bx := p.button(x, y, "↵", "Apply", true, right)
	bx = p.button(bx+2, y, "space", "Pick", false, right)
	bx = p.button(bx+2, y, "x", "Clear", false, right)
	p.button(bx+2, y, "esc", "Close", false, right)
}

func (a *tuiApp) drawHelp() {
	p := a.p
	cols := []struct {
		title string
		keys  [][2]string
	}{
		{"FIND", [][2]string{{"type", "search as you type"}, {"tab", "this project / all / kept"}, {"↑↓ 1-9", "pick a session"}, {"↑ on empty", "past searches"}, {"a", "filter by agent"}}},
		{"READ", [][2]string{{"↵", "open at the match"}, {"n N", "next / previous match"}, {"t", "unfold long messages"}, {"g G", "top / bottom"}, {"esc", "back to the list"}}},
		{"ACT", [][2]string{{"r", "resume in its agent"}, {"o", "continue in any agent"}, {"c", "copy the context"}, {"R", "put a deleted one back"}, {"^k", "every command by name"}}},
	}
	x, y, iw := a.modalBox(100, 12)
	right := x + iw
	cx := p.Put(x, y, "Keys", bold(cText), right)
	p.Put(cx+3, y, "the three you need: type, ↵, o", fgs(cMuted), right)
	y += 2
	colW := iw / 3
	for c, col := range cols {
		cx := x + c*colW
		p.Put(cx, y, col.title, fgs(cMuted), cx+colW)
		for i, k := range col.keys {
			kx := p.Put(cx, y+1+i, " "+k[0]+" ", boldOn(cText, cSurf2), cx+colW)
			p.PutClip(kx+1, y+1+i, k[1], fgs(cSub), cx+colW-1)
		}
	}
	p.Put(x, y+7, "The mouse works too: click a card, double-click to open, scroll anywhere.", fgs(cMuted), right)
}
