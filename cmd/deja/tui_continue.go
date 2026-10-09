package main

import (
	"os"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// The continue-in picker: every agent deja can hand a session to. The ones
// the reader continued into before lead, then the ones on this machine, which
// open with the context as their first prompt; the rest get it on the
// clipboard and stay folded into one line until asked for or typed for.

const tuiContinuedMax = 3

func tuiContinuedPath(dir string) string { return dir + ".tui-continued" }

// loadTUIContinued is the agents picked here before, newest first.
func loadTUIContinued(dir string) []string {
	b, err := os.ReadFile(tuiContinuedPath(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); agentNames[l] != "" && len(out) < tuiContinuedMax {
			out = append(out, l)
		}
	}
	return out
}

// rememberContinued puts the agent just picked at the front of the recent ones.
func (a *tuiApp) rememberContinued(id string) {
	ids := []string{id}
	for _, h := range a.continued {
		if h != id && len(ids) < tuiContinuedMax {
			ids = append(ids, h)
		}
	}
	a.continued = ids
	_ = atomicfile.Write(tuiContinuedPath(a.dir), []byte(strings.Join(ids, "\n")+"\n"), 0o600)
}

func (a *tuiApp) openContinue() {
	a.remember()
	s, ok := a.current()
	if !ok {
		return
	}
	a.openModal(modalContinue)
	a.m.src = s
	used := map[string]int{}
	for _, ac := range a.agentsAll {
		used[ac.h] = ac.n
	}
	a.m.targets = withRecent(continueTargets(nil, used), a.continued)
	a.want(s)
}

// withRecent moves the agents continued into before to the front, newest
// first, marked so the newest can say it was used last.
func withRecent(ts []continueTarget, recent []string) []continueTarget {
	var front, rest []continueTarget
	for _, id := range recent {
		for _, t := range ts {
			if t.id == id {
				t.recent = true
				front = append(front, t)
			}
		}
	}
	for _, t := range ts {
		if !slices.Contains(recent, t.id) {
			rest = append(rest, t)
		}
	}
	return append(front, rest...)
}

// The blocks the picker draws, in order, a blank line apart. The recent
// agents carry no block of their own: they lead the ones on this machine.
const (
	groupHere = iota
	groupElse
	groupMore // the one line the folded agents collapse to
)

func (t continueTarget) group() int {
	switch {
	case t.more > 0:
		return groupMore
	case t.recent || t.installed && !t.paste:
		return groupHere
	}
	return groupElse
}

// shownTargets is the list under the typed filter. With no filter the agents
// not on this machine fold into one line until it is opened; a filter
// searches every agent.
func (a *tuiApp) shownTargets() []continueTarget {
	f := strings.ToLower(string(a.m.filter))
	var out []continueTarget
	folded := 0
	for _, t := range a.m.targets {
		if f == "" && !a.m.expanded && t.group() == groupElse {
			folded++
			continue
		}
		if f == "" || strings.Contains(strings.ToLower(agentName(t.id)+" "+t.id), f) {
			out = append(out, t)
		}
	}
	if folded > 0 {
		out = append(out, continueTarget{more: folded})
	}
	return out
}

// gridCols is how many columns of agents fit the modal: two at most, so the
// names have room around them.
func gridCols(iw int) int {
	if iw >= 40 {
		return 2
	}
	return 1
}

// continueGrid puts each target on a row and a column, and each row on a line
// of the modal. A block starts on a row of its own with a blank line above
// it; the folded line takes the next cell, as the last entry of the list.
// pos is the row and column, line the line.
func continueGrid(ts []continueTarget, cols int) (pos [][2]int, line []int) {
	r, c, l, prev := 0, 0, 0, -1
	for i, t := range ts {
		g := t.group()
		if g != prev && g != groupMore {
			if i > 0 {
				if c != 0 {
					r, c, l = r+1, 0, l+1
				}
				l++
			}
			prev = g
		}
		pos = append(pos, [2]int{r, c})
		line = append(line, l)
		if c++; c == cols || g == groupMore {
			r, c, l = r+1, 0, l+1
		}
	}
	return pos, line
}

// gridMove steps a selection one row up or down, keeping its column where
// the next row is long enough and taking that row's last entry where not.
func gridMove(pos [][2]int, sel, dr int) int {
	if sel < 0 || sel >= len(pos) {
		return sel
	}
	want, col := pos[sel][0]+dr, pos[sel][1]
	best := sel
	for i, p := range pos {
		if p[0] == want && p[1] <= col {
			best = i
		}
	}
	return best
}

func (a *tuiApp) modalMove(dr int) int {
	if a.modal == modalContinue {
		pos, _ := continueGrid(a.shownTargets(), a.m.cols)
		return gridMove(pos, a.m.sel, dr)
	}
	return a.m.sel + dr*3
}

func (a *tuiApp) drawContinue() {
	p := a.p
	ts := a.shownTargets()
	a.m.cols = gridCols(min(76, p.W-4) - 4)
	pos, line := continueGrid(ts, a.m.cols)
	total := 1
	if len(ts) > 0 {
		total = line[len(ts)-1] + 1
	}
	const chrome = 11 // title, filter, note, button, the gaps and the border
	room := min(total, p.H-2-chrome)
	x, y, iw := a.modalBox(76, room+chrome)
	right := x + iw
	cx := p.Put(x, y, "Continue this session in…", bold(cText), right)
	p.PutClip(cx+3, y, "from "+agentName(a.m.src.Harness)+" · "+tuiProject(a.m.src), fgs(cMuted), right)
	y += 2
	p.Fill(x, y, iw, 1, cSurf2)
	fx := p.Put(x+1, y, "⌕ ", on(cAcc, cSurf2), right)
	if len(a.m.filter) == 0 {
		fx = p.Put(fx, y, "▏", on(cAcc, cSurf2), right)
		p.Put(fx, y, "type to filter "+num(len(a.m.targets))+" agents", on(cMuted, cSurf2), right)
	} else {
		fx = p.Put(fx, y, string(a.m.filter), boldOn(cText, cSurf2), right)
		p.Put(fx, y, "▏", on(cAcc, cSurf2), right)
	}
	y += 2
	first := 0
	if len(ts) > 0 {
		if sl := line[a.m.sel]; sl >= room {
			first = sl - room + 1
		}
	}
	colW := iw / a.m.cols
	for i, t := range ts {
		yy := y + line[i] - first
		if line[i] < first || line[i]-first >= room {
			continue
		}
		if t.more > 0 {
			a.foldEntry(x+pos[i][1]*colW, yy, colW, t.more, i == a.m.sel)
			continue
		}
		extra := ""
		if t.paste {
			extra = "paste"
		}
		ex := x + pos[i][1]*colW
		a.gridEntry(ex, yy, colW, t.id, i == a.m.sel, !t.installed, extra)
		if t.recent && t.id == a.continued[0] {
			st := on(cFaint, cSurf)
			if i == a.m.sel {
				st = on(cMuted, cOver)
			}
			p.Put(ex+5+termwidth.Columns(agentName(t.id)), yy, "last used", st, ex+colW-1)
		}
	}
	if len(ts) == 0 {
		p.Put(x, y, "No agent by that name.", fgs(cFaint), right)
	}
	last := 0
	if len(ts) > 0 {
		last = line[len(ts)-1]
	}
	y += min(last+1-first, room) + 1
	label := "Open"
	note := "Starts it here with what this session decided and left open."
	if a.m.sel < len(ts) {
		t := ts[a.m.sel]
		label = "Open in " + agentName(t.id)
		switch {
		case t.more > 0:
			label = "Show them"
			note = "They get the context on the clipboard; typing a name finds them."
		case t.paste || !t.installed:
			label = "Copy for " + agentName(t.id)
			note = "Copies the context, to paste as its first message."
		}
	}
	p.PutClip(x, y, note, fgs(cSub), right)
	y += 2
	bx := p.button(x, y, "↵", label, true, right)
	p.button(bx+2, y, "esc", "Cancel", false, right)
}

// foldEntry is the one line the agents not on this machine fold into.
func (a *tuiApp) foldEntry(x, y, w, n int, sel bool) {
	p := a.p
	bg := cSurf
	if sel {
		bg = cOver
		p.Fill(x, y, w-1, 1, bg)
	}
	p.Put(x+3, y, "+"+num(n)+" not installed here", on(cMuted, bg), x+w)
}
