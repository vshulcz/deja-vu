package main

import (
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/mark"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The first run builds the index behind the screen instead of before it:
// the cat wags while the agents' history is read, each agent ticks in as it
// lands, and once the newest sessions are searchable the reader can go in
// while the rest is still being read.

// welcomeState is the first run's progress.
type welcomeState struct {
	started time.Time
	took    time.Duration
	done    bool
	err     error
	early   bool // went in before the build finished
	prog    *tuiProgress
}

// tuiProgress takes the build's reports. The build parses stores in
// parallel, so it is read under a lock from the draw rather than posted.
type tuiProgress struct {
	mu          sync.Mutex
	phase       string
	total, done int
	stores      []agentCount
}

func (p *tuiProgress) Phase(name string, total int) {
	p.mu.Lock()
	p.phase, p.total, p.done = name, total, 0
	p.mu.Unlock()
}

func (p *tuiProgress) Advance(n int) {
	p.mu.Lock()
	p.done += n
	p.mu.Unlock()
}

func (p *tuiProgress) Harness(name string, sessions, _ int) {
	if sessions == 0 {
		return
	}
	p.mu.Lock()
	p.stores = append(p.stores, agentCount{h: name, n: sessions})
	p.mu.Unlock()
}

func (p *tuiProgress) snapshot() (string, int, int, []agentCount) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase, p.total, p.done, append([]agentCount(nil), p.stores...)
}

func (a *tuiApp) firstBuild() {
	prog := &tuiProgress{}
	start := time.Now()
	a.post(func() { a.welcome.started, a.welcome.prog, a.indexing = start, prog, true })
	index.SetProgress(prog)
	index.SuppressHarnessNarration = true
	err := index.Ensure(a.dir, "", false, io.Discard)
	index.SetProgress(nil)
	index.SuppressHarnessNarration = false
	took := time.Since(start)
	a.post(func() {
		a.welcome.done, a.welcome.err, a.welcome.took = true, err, took
		a.indexing = false
		if a.view != viewWelcome {
			// Went in early: what is on screen is the reader's, so the rest
			// of the history joins it without resetting a search or a pick.
			a.reload()
		} else {
			a.loadHome()
			if a.scope == scopeHere && a.total == 0 {
				a.scope = scopeAll
				a.loadHome()
			}
		}
		if a.welcome.early && err == nil {
			a.say("All "+grouped(len(a.allMeta))+" sessions are in.", true)
		}
	})
	if err == nil {
		a.spawn(a.loadKept)
		a.spawn(a.loadBehind)
	}
}

// earlyReady says the newest sessions are already searchable: a large first
// build publishes them before it reads the rest.
func (a *tuiApp) earlyReady() bool {
	return !a.welcome.done && index.HasManifest(a.dir)
}

func (a *tuiApp) handleWelcome(ev tui.Event) {
	if ev.Kind != tui.EvKey {
		return
	}
	if ev.Key == tui.KeyEsc || (ev.Key == tui.KeyRune && ev.Rune == 'q') {
		a.quit = true
		return
	}
	switch {
	case a.welcome.done:
	case a.earlyReady() && (ev.Key == tui.KeyEnter || ev.Key == tui.KeyRune):
		a.welcome.early = true
	default:
		// A key pressed into a build that cannot be entered yet still gets
		// an answer, so the screen does not look like it ignored it.
		a.say("Still reading. You can go in as soon as the newest sessions are searchable.", true)
		return
	}
	a.view = viewList
	a.loadHome()
	if a.scope == scopeHere && a.total == 0 {
		a.scope = scopeAll
		a.loadHome()
	}
	if ev.Key == tui.KeyRune && ev.Rune != ' ' {
		// Typing straight away searches; nothing typed is lost.
		a.handleList(ev)
	}
}

// wagFrame is the tail's position at a moment: the cycle in mark, about
// three positions a second.
func wagFrame(now time.Time) mark.Mood {
	m := mark.Ready
	m.TailSet = mark.WagCycle[int(now.UnixMilli()/320)%len(mark.WagCycle)]
	return m
}

var phaseLabel = map[string]string{
	"finding transcripts":       "Finding your agents' transcripts",
	"reading sessions":          "Reading sessions",
	"indexing messages":         "Indexing what was said",
	"mining fixes and commands": "Finding fixes and commands",
	"writing index":             "Writing the index",
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
	y := max(1, (p.H-16)/2)
	switch {
	case p.W >= 84:
		a.drawCat(tx, y, mood)
		tx += cw + 6
	case p.H >= 28:
		// Too narrow to sit beside the text: the cat goes above it.
		y = max(1, (p.H-28)/2)
		a.drawCat(tx, y, mood)
		y += 12
	}
	right := p.W - 2
	x := p.Put(tx, y+1, "deja", bold(cText), right)
	p.PutClip(x+2, y+1, "everything your coding agents did, in one place", fgs(cSub), right)
	ly := y + 3
	switch {
	case !w.done:
		a.drawBuilding(tx, ly, right)
	case w.err != nil:
		p.PutClip(tx, ly, "Could not read the history: "+w.err.Error(), fgs(cPeach), right)
		p.Put(tx, ly+2, "esc quits; deja doctor says what is wrong.", fgs(cMuted), right)
	case len(a.allMeta) == 0:
		p.Put(tx, ly, "No agent history on this machine yet.", bold(cText), right)
		p.PutClip(tx, ly+2, "Work with any of "+num(len(agentNames))+" agents and it shows up here.", fgs(cSub), right)
		p.Put(tx, ly+4, "esc quits", fgs(cMuted), right)
	default:
		ly = a.drawStores(tx, ly, right, a.agentsAll, true)
		ly++
		cx := p.Put(tx, ly, "Indexed ", fgs(cText), right)
		cx = p.Put(cx, ly, grouped(len(a.allMeta))+" sessions", bold(cText), right)
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

// drawBuilding is the build in progress: what it is doing, how far along,
// and the agents read so far.
func (a *tuiApp) drawBuilding(tx, ly, right int) {
	p := a.p
	w := a.welcome
	phase, total, done := "", 0, 0
	var stores []agentCount
	if w.prog != nil {
		phase, total, done, stores = w.prog.snapshot()
	}
	label := phaseLabel[phase]
	if label == "" {
		label = "Reading your agents' history"
	}
	dots := []string{"   ", ".  ", ".. ", "..."}[int(a.now.UnixMilli()/400)%4]
	p.Put(tx, ly, label+dots, bold(cText), right)
	ly++
	barW := min(40, right-tx-8)
	if barW >= 10 {
		frac := -1.0
		if total > 0 {
			frac = min(1, float64(done)/float64(total))
		}
		a.drawBar(tx, ly, barW, frac)
		if frac >= 0 {
			p.Put(tx+barW+1, ly, strconv.Itoa(int(frac*100))+"%", fgs(cSub), right)
		}
	}
	ly += 2
	ly = a.drawStores(tx, ly, right, stores, false)
	ly++
	if !w.started.IsZero() {
		p.Put(tx, ly, num(int(a.now.Sub(w.started).Seconds()))+"s · first time only, after this deja opens straight away", fgs(cMuted), right)
	}
	if a.earlyReady() {
		bx := p.button(tx, ly+2, "↵", "start now", true, right)
		p.PutClip(bx+2, ly+2, "the newest sessions are searchable, the rest keeps reading", fgs(cSub), right)
	} else if a.toast != "" && a.now.Before(a.toastUntil) {
		p.PutClip(tx, ly+2, a.toast, fgs(cSub), right)
	}
}

// drawBar is a thin progress bar; a negative fraction is a stage of unknown
// length, drawn as a bead sliding back and forth.
func (a *tuiApp) drawBar(x, y, w int, frac float64) {
	p := a.p
	for i := 0; i < w; i++ {
		p.Put(x+i, y, "━", fgs(cOver), x+w)
	}
	if frac < 0 {
		span := 2 * (w - 6)
		pos := int(a.now.UnixMilli()/60) % max(1, span)
		if pos >= w-6 {
			pos = span - pos
		}
		for i := 0; i < 6; i++ {
			p.Put(x+pos+i, y, "━", fgs(cAcc), x+w)
		}
		return
	}
	n := int(frac * float64(w))
	for i := 0; i < n; i++ {
		p.Put(x+i, y, "━", fgs(cAcc), x+w)
	}
}

// drawSkeleton stands in for text still being read: n bars of uneven length
// with a light that runs across them, so a wait looks like work.
func (a *tuiApp) drawSkeleton(x, y, w, n int) {
	p := a.p
	if w < 4 {
		return
	}
	widths := []int{90, 75, 95, 60, 85, 40}
	sweep := int(a.now.UnixMilli()/40) % (w + 20)
	for i := 0; i < n; i++ {
		bw := max(3, w*widths[i%len(widths)]/100)
		for c := 0; c < bw; c++ {
			col := cSurf2
			if d := c - (sweep - 10 - i*2); d >= 0 && d < 6 {
				col = cOver
			}
			p.Put(x+c, y+i, "▀", fgs(col), x+w)
		}
	}
}

// drawStores lists the agents read so far, one row each.
func (a *tuiApp) drawStores(tx, ly, right int, stores []agentCount, all bool) int {
	p := a.p
	for i, ac := range stores {
		if i == 8 {
			p.Put(tx+2, ly, "+"+num(len(stores)-8)+" more", fgs(cMuted), right)
			return ly + 1
		}
		cx := p.Put(tx, ly, "✓", fgs(cGrn), right)
		cx = p.Put(cx+1, ly, "●", fgs(agentColor(ac.h)), right)
		p.PutClip(cx+1, ly, agentName(ac.h), fgs(cText), tx+22)
		n := grouped(ac.n)
		p.Put(tx+30-len(n), ly, n, fgs(cSub), right)
		p.Put(tx+31, ly, "sessions", fgs(cMuted), right)
		ly++
	}
	return ly
}

func formatSeconds(d time.Duration) string {
	s := d.Seconds()
	if s < 10 {
		return num(int(s)) + "." + num(int(s*10)%10) + "s"
	}
	return num(int(s)) + "s"
}

// grouped writes a count with thousands separators: 27,000 reads at a
// glance where 27000 does not.
func grouped(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + grouped(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
