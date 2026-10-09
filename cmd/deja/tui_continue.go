package main

import "strings"

// The continue-in picker: every agent deja can hand a session to, the ones on
// this machine first. Those open with the context as their first prompt; the
// rest get it on the clipboard.

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
	a.m.targets = continueTargets(nil, used)
	a.want(s)
}

// shownTargets is the list under the typed filter.
func (a *tuiApp) shownTargets() []continueTarget {
	f := strings.ToLower(string(a.m.filter))
	var out []continueTarget
	for _, t := range a.m.targets {
		if f == "" || strings.Contains(strings.ToLower(agentName(t.id)+" "+t.id), f) {
			out = append(out, t)
		}
	}
	return out
}

// gridCols is how many columns of agents fit the modal.
func gridCols(iw int) int {
	if iw >= 66 {
		return 3
	}
	if iw >= 40 {
		return 2
	}
	return 1
}

// continueGrid puts each target on a row and a column. The installed ones
// come first and the rest start on a row of their own, so the two groups
// never share one. split is the first target of the second group, -1 when
// there is none.
func continueGrid(ts []continueTarget, cols int) (pos [][2]int, split int) {
	split = -1
	r, c := 0, 0
	for i, t := range ts {
		if !t.installed && split < 0 {
			split = i
			if c != 0 {
				r, c = r+1, 0
			}
		}
		pos = append(pos, [2]int{r, c})
		if c++; c == cols {
			r, c = r+1, 0
		}
	}
	return pos, split
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
	a.m.cols = gridCols(min(88, p.W-4) - 4)
	pos, split := continueGrid(ts, a.m.cols)
	// Visual lines: a label over each group, a blank line between them.
	line := func(i int) int {
		l := pos[i][0] + 1
		if split > 0 && i >= split {
			l += 2
		}
		return l
	}
	total := 1
	if len(ts) > 0 {
		total = line(len(ts)-1) + 1
	}
	const chrome = 11 // title, filter, note, button, the gaps and the border
	room := min(total, p.H-2-chrome)
	x, y, iw := a.modalBox(88, room+chrome)
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
		if sl := line(a.m.sel); sl >= room {
			first = sl - room + 1
		}
	}
	put := func(l int, draw func(yy int)) {
		if l-first >= 0 && l-first < room {
			draw(y + l - first)
		}
	}
	colW := iw / a.m.cols
	if split != 0 && len(ts) > 0 {
		put(0, func(yy int) { p.Put(x, yy, "ON THIS MACHINE", fgs(cMuted), right) })
	}
	if split >= 0 {
		l := line(split) - 1
		put(l, func(yy int) {
			lx := p.Put(x, yy, "ALSO SUPPORTED", fgs(cMuted), right)
			p.PutClip(lx+2, yy, "not installed here · they get the context on the clipboard", fgs(cFaint), right)
		})
	}
	last := 0
	for i, t := range ts {
		l := line(i)
		last = l
		extra := ""
		if t.paste {
			extra = "paste"
		}
		i, t := i, t
		put(l, func(yy int) {
			a.gridEntry(x+pos[i][1]*colW, yy, colW, t.id, i == a.m.sel, !t.installed, extra)
		})
	}
	if len(ts) == 0 {
		p.Put(x, y, "No agent by that name.", fgs(cFaint), right)
	}
	y += min(last+1-first, room) + 1
	label := "Open"
	note := "Starts the agent here with what this session asked, decided and left open."
	if a.m.sel < len(ts) {
		t := ts[a.m.sel]
		label = "Open in " + agentName(t.id)
		if t.paste || !t.installed {
			label = "Copy for " + agentName(t.id)
			note = "Copies what this session asked, decided and left open, to paste as the first message."
		}
	}
	p.PutClip(x, y, note, fgs(cSub), right)
	y += 2
	bx := p.button(x, y, "↵", label, true, right)
	p.button(bx+2, y, "esc", "Cancel", false, right)
}
