package main

import (
	"io"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/mark"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The first run builds the index behind the screen instead of before it:
// the cat wags while the agents' history is read, then says what it found.

// welcomeState is the first run's progress.
type welcomeState struct {
	started time.Time
	took    time.Duration
	done    bool
	err     error
}

func (a *tuiApp) firstBuild() {
	a.post(func() { a.welcome.started = time.Now() })
	start := time.Now()
	err := index.Ensure(a.dir, "", false, io.Discard)
	took := time.Since(start)
	a.post(func() {
		a.welcome.done, a.welcome.err, a.welcome.took = true, err, took
		a.loadHome()
		if a.scope == scopeHere && a.total == 0 {
			a.scope = scopeAll
			a.loadHome()
		}
	})
	if err == nil {
		go a.loadKept()
		go a.loadBehind()
	}
}

func (a *tuiApp) handleWelcome(ev tui.Event) {
	if ev.Kind != tui.EvKey {
		return
	}
	if ev.Key == tui.KeyEsc || (ev.Key == tui.KeyRune && ev.Rune == 'q') {
		a.quit = true
		return
	}
	if a.welcome.done {
		a.view = viewList
		a.loadHome()
		if ev.Key == tui.KeyRune && ev.Rune != ' ' {
			// Typing straight away searches; nothing typed is lost.
			a.handleList(ev)
		}
	}
}

// wagFrame is the tail's position at a moment: the cycle in mark, about
// three positions a second.
func wagFrame(now time.Time) mark.Mood {
	m := mark.Ready
	m.TailSet = mark.WagCycle[int(now.UnixMilli()/320)%len(mark.WagCycle)]
	return m
}

func (a *tuiApp) drawWelcome() {
	p := a.p
	w := a.welcome
	mood := wagFrame(a.now)
	if w.done {
		mood = mark.Ready
		if w.err != nil || len(a.allMeta) == 0 {
			mood = mark.Asleep
		}
	}
	cw := 24
	tx := (p.W - cw - 6 - 52) / 2
	if tx < 2 {
		tx = 2
	}
	y := max(1, (p.H-14)/2)
	if p.W >= 84 {
		a.drawCat(tx, y, mood)
		tx += cw + 6
	}
	right := p.W - 2
	x := p.Put(tx, y+1, "deja", bold(cText), right)
	p.PutClip(x+2, y+1, "everything your coding agents did, in one place", fgs(cSub), right)
	ly := y + 3
	switch {
	case !w.done:
		dots := []string{"   ", ".  ", ".. ", "..."}[int(a.now.UnixMilli()/400)%4]
		p.Put(tx, ly, "Reading your agents' history"+dots, bold(cText), right)
		p.Put(tx, ly+2, "First time only. After this, deja opens straight away.", fgs(cMuted), right)
		if !w.started.IsZero() {
			p.Put(tx, ly+3, num(int(a.now.Sub(w.started).Seconds()))+"s", fgs(cFaint), right)
		}
	case w.err != nil:
		p.PutClip(tx, ly, "Could not read the history: "+w.err.Error(), fgs(cPeach), right)
		p.Put(tx, ly+2, "esc quits; deja doctor says what is wrong.", fgs(cMuted), right)
	case len(a.allMeta) == 0:
		p.Put(tx, ly, "No agent history on this machine yet.", bold(cText), right)
		p.PutClip(tx, ly+2, "Work with any of "+num(len(agentNames))+" agents and it shows up here.", fgs(cSub), right)
		p.Put(tx, ly+4, "esc quits", fgs(cMuted), right)
	default:
		for i, ac := range a.agentsAll {
			if i == 8 {
				p.Put(tx+2, ly, "+"+num(len(a.agentsAll)-8)+" more", fgs(cMuted), right)
				ly++
				break
			}
			cx := p.Put(tx, ly, "✓", fgs(cGrn), right)
			cx = p.Put(cx+1, ly, "●", fgs(agentColor(ac.h)), right)
			p.PutClip(cx+1, ly, agentName(ac.h), fgs(cText), tx+22)
			n := num(ac.n)
			p.Put(tx+28-len(n), ly, n, fgs(cSub), right)
			p.Put(tx+29, ly, "sessions", fgs(cMuted), right)
			ly++
		}
		ly++
		cx := p.Put(tx, ly, "Indexed ", fgs(cText), right)
		cx = p.Put(cx, ly, tuiCount(len(a.allMeta), "session"), bold(cText), right)
		cx = p.Put(cx, ly, " from "+tuiCount(len(a.agentsAll), "agent")+" in ", fgs(cText), right)
		p.Put(cx, ly, formatSeconds(w.took), bold(cText), right)
		if len(a.kept) > 0 {
			ly++
			kx := p.Put(tx, ly, num(len(a.kept)), bold(cPeach), right)
			p.PutClip(kx+1, ly, "of them already deleted by their agent, still here", fgs(cSub), right)
		}
		p.button(tx, ly+2, "↵", "start", true, right)
	}
}

func formatSeconds(d time.Duration) string {
	s := d.Seconds()
	if s < 10 {
		return num(int(s)) + "." + num(int(s*10)%10) + "s"
	}
	return num(int(s)) + "s"
}
