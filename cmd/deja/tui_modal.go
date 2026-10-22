package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

type modalState struct {
	sel      int
	cols     int  // the continue grid's, as last drawn
	expanded bool // the continue grid shows the agents not installed here
	filter   []rune
	src      model.Session
	targets  []continueTarget
	picked   map[string]bool
	ids      []string
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
	if a.modal == modalNews {
		a.handleNews(ev)
		return
	}
	if a.modal == modalForget {
		if ev.Kind == tui.EvKey {
			a.handleForget(ev)
		}
		return
	}
	if a.modal == modalDelete {
		if ev.Kind == tui.EvKey {
			a.handleDelete(ev)
		}
		return
	}
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
		if ts[a.m.sel].more > 0 {
			a.m.expanded = true
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
	if t.id == chatgptApp {
		a.after = func() error { return openInCodexApp(s, runtime.GOOS) }
		a.leaving = "Opening in the ChatGPT app…"
		a.quit = true
		return
	}
	a.rememberContinued(t.id)
	if t.paste || !t.installed {
		a.copySession(s, "paste it into "+agentName(t.id))
		return
	}
	a.after = func() error {
		fmt.Fprintf(os.Stderr, "deja: continuing in %s\n", agentName(t.id))
		pickedOnScreen = &s
		return runHandoff(a.dir, []string{"--to", t.id, s.ID, "--exec"}, os.Stdout)
	}
	a.leaving = "Continuing in " + agentName(t.id) + "…"
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
	if err := a.clip(text); err != nil {
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
	case modalNews:
		a.drawNews()
	case modalForget:
		a.drawForget()
	case modalDelete:
		a.drawDelete()
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
		a.gridEntry(bx-1, y+r, colW-(bx-1-ex), h, i == a.m.sel, false, grouped(counts[h]))
	}
	y += rows + 1
	bx := p.button(x, y, "↵", "Apply", true, right)
	bx = p.button(bx+2, y, "space", "Pick", false, right)
	bx = p.button(bx+2, y, "x", "Clear", false, right)
	p.button(bx+2, y, "esc", "Close", false, right)
}

// helpSection is one titled group of keys on the help screen.
type helpSection struct {
	title string
	keys  [][2]string
}

// drawHelp lays the sections out as columns sized to what they hold, keys
// right-aligned so every label in a column starts on one edge. Three equal
// thirds cut "this project / all / deleted" short at any width and most labels
// at 80 columns; where three do not fit, the sections stack into two.
func (a *tuiApp) drawHelp() {
	p := a.p
	secs := []helpSection{
		{"FIND", [][2]string{{"type", "search as you type"}, {"tab", "this project / all / deleted"}, {"↑↓ 1-9", "pick a session"}, {"↑ on empty", "past searches"}, {"^s", "best match / newest first"}, {"a", "filter by agent"}}},
		{"READ", [][2]string{{"↵", "open at the match"}, {"/", "find in the session"}, {"n N", "next / previous match"}, {"] [", "next / previous turn"}, {"t", "unfold long messages"}, {"g G", "top / bottom"}, {"esc", "back to the list"}}},
		{"ACT", [][2]string{{"r", "resume in its agent"}, {"o", "continue in any agent"}, {"c", "copy the context"}, {"D", "delete everywhere"}, {"R", "put a deleted one back"}, {"^k", "copy id, path, forget…"}}},
	}
	gap := 4
	cols := helpColumns(secs, p.W-8-2*gap)
	keyW, colW := make([]int, len(cols)), make([]int, len(cols))
	inner, tall := 0, 0
	for c, col := range cols {
		labelW, h := 0, 0
		for _, i := range col {
			for _, k := range secs[i].keys {
				keyW[c] = max(keyW[c], termwidth.Columns(k[0])+2)
				labelW = max(labelW, termwidth.Columns(k[1]))
			}
			h += len(secs[i].keys) + 2
		}
		colW[c] = keyW[c] + 1 + labelW
		inner += colW[c]
		tall = max(tall, h-1)
	}
	// The gutter gives way before a label is cut.
	if spare := p.W - 8 - inner; spare < gap*(len(cols)-1) {
		gap = max(2, spare/max(1, len(cols)-1))
	}
	inner += gap * (len(cols) - 1)
	filters := [][2]string{{"codex:", "one agent"}, {"today", "yesterday, week, month"}, {"in:api", "one project"}}
	fw := len("FILTERS") + 2 - 3
	for _, f := range filters {
		fw += termwidth.Columns(f[0]) + 1 + termwidth.Columns(f[1]) + 3
	}
	boxW := max(inner, 76) + 4
	h := tall + 8
	if fw > min(boxW, p.W-4)-4 {
		h++ // the filters take two lines
	}
	x, y, iw := a.modalBox(boxW, h)
	right := x + iw
	cx := p.Put(x, y, "Keys", bold(cText), right)
	p.Put(cx+3, y, "the three you need: type, ↵, o", fgs(cMuted), right)
	y += 2
	cx = x
	for c, col := range cols {
		cy := y
		for _, i := range col {
			p.Put(cx, cy, secs[i].title, fgs(cMuted), right)
			for j, k := range secs[i].keys {
				kx := cx + keyW[c] - termwidth.Columns(k[0]) - 2
				p.Put(kx, cy+1+j, " "+k[0]+" ", boldOn(cText, cSurf2), right)
				p.PutClip(cx+keyW[c]+1, cy+1+j, k[1], fgs(cSub), min(cx+colW[c], right))
			}
			cy += len(secs[i].keys) + 2
		}
		cx += colW[c] + gap
	}
	// The words the box takes as filters, each drawn as the chip it becomes;
	// one that does not fit goes to the next line rather than being cut.
	fy := y + tall + 1
	indent := p.Put(x, fy, "FILTERS", fgs(cMuted), right) + 2
	fx := indent
	chip := tui.Style{FG: cAcc, BG: cOver, Bold: true, Reverse: p.mono}
	for _, f := range filters {
		if w := termwidth.Columns(f[0]) + 1 + termwidth.Columns(f[1]); fx > indent && fx+w > right {
			fx, fy = indent, fy+1
		}
		fx = p.Put(fx, fy, f[0], chip, right)
		fx = p.Put(fx+1, fy, f[1], fgs(cSub), right) + 3
	}
	p.PutClip(x, fy+2, "Mouse: click a card, double-click to open it, scroll anywhere.", fgs(cMuted), right)
}

// helpColumns puts the sections side by side when their natural widths fit
// in room, and otherwise stacks them into two columns, each section going
// under whichever column is shorter so far.
func helpColumns(secs []helpSection, room int) [][]int {
	width := func(s helpSection) int {
		kw, lw := 0, 0
		for _, k := range s.keys {
			kw = max(kw, termwidth.Columns(k[0])+2)
			lw = max(lw, termwidth.Columns(k[1]))
		}
		return kw + 1 + lw
	}
	total := 0
	for _, s := range secs {
		total += width(s)
	}
	n := len(secs)
	if total > room {
		n = 2
	}
	cols, h := make([][]int, n), make([]int, n)
	for i, s := range secs {
		c := 0
		for k := range h {
			if h[k] < h[c] {
				c = k
			}
		}
		cols[c] = append(cols[c], i)
		h[c] += len(s.keys) + 2
	}
	return cols
}
