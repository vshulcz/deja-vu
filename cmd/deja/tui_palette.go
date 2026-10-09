package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The command palette: every action by name, with its key beside it so the
// palette also teaches the keys.

type paletteItem struct {
	label, key string
	do         func()
}

func (a *tuiApp) paletteItems() []paletteItem {
	var out []paletteItem
	add := func(label, key string, do func()) { out = append(out, paletteItem{label, key, do}) }
	if s, ok := a.current(); ok {
		d := a.details[sessionKey(s)]
		if a.view != viewReader {
			add("Read this session", "↵", a.openReader)
		}
		if d != nil && d.gone {
			add("Put it back where "+agentName(s.Harness)+" finds it", "R", a.putBack)
		} else {
			add("Resume in "+agentName(s.Harness), "r", a.resumeSelected)
		}
		add("Continue in another agent…", "o", a.openContinue)
		add("Copy the context for any agent", "c", a.copyContext)
	}
	names := []string{"This project", "All projects", "Deleted sessions"}
	for i, n := range names {
		if i != a.scope {
			scope := i
			add("Show "+strings.ToLower(n[:1])+n[1:], "tab", func() { a.view = viewList; a.setScope(scope) })
		}
	}
	add("Filter by agent…", "a", func() { a.openModal(modalAgents) })
	if len(a.filter) > 0 {
		add("Show every agent", "", func() { a.filter = map[string]bool{}; a.reload() })
	}
	for i, ac := range a.agentsAll {
		if i == 6 {
			break
		}
		h := ac.h
		add("Show only "+agentName(h), "", func() { a.view = viewList; a.filter = map[string]bool{h: true}; a.reload() })
	}
	add("Keys", "?", func() { a.openModal(modalHelp) })
	add("Quit", "esc", func() { a.quit = true })
	return out
}

// shownPalette is the items under the typed filter: every typed word has to
// appear in the label.
func (a *tuiApp) shownPalette() []paletteItem {
	words := strings.Fields(strings.ToLower(string(a.m.filter)))
	var out []paletteItem
	for _, it := range a.paletteItems() {
		label := strings.ToLower(it.label)
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(label, w)
		}
		if ok {
			out = append(out, it)
		}
	}
	return out
}

func (a *tuiApp) openPalette() {
	a.openModal(modalPalette)
}

func (a *tuiApp) handlePalette(ev tui.Event) {
	items := a.shownPalette()
	switch ev.Key {
	case tui.KeyEsc:
		a.modal = modalNone
	case tui.KeyUp:
		a.m.sel--
	case tui.KeyDown, tui.KeyTab:
		a.m.sel++
	case tui.KeyBackspace:
		if n := len(a.m.filter); n > 0 {
			a.m.filter, a.m.sel = a.m.filter[:n-1], 0
		}
	case tui.KeyRune:
		a.m.filter, a.m.sel = append(a.m.filter, ev.Rune), 0
	case tui.KeyEnter:
		if a.m.sel < len(items) {
			a.modal = modalNone
			items[a.m.sel].do()
		}
		return
	}
	a.m.sel = max(0, min(a.m.sel, len(items)-1))
}

func (a *tuiApp) drawPalette() {
	p := a.p
	items := a.shownPalette()
	rows := min(len(items), p.H-10)
	x, y, iw := a.modalBox(72, max(rows, 1)+7)
	right := x + iw
	p.Fill(x, y, iw, 1, cSurf2)
	fx := p.Put(x+1, y, "› ", on(cAcc, cSurf2), right)
	if len(a.m.filter) == 0 {
		fx = p.Put(fx, y, "▏", on(cAcc, cSurf2), right)
		p.Put(fx, y, "type a command", on(cMuted, cSurf2), right)
	} else {
		fx = p.Put(fx, y, string(a.m.filter), boldOn(cText, cSurf2), right)
		p.Put(fx, y, "▏", on(cAcc, cSurf2), right)
	}
	y += 2
	first := max(0, a.m.sel-rows+1)
	for i := first; i < len(items) && i-first < rows; i++ {
		it := items[i]
		bg := cSurf
		if i == a.m.sel {
			bg = cOver
			p.Fill(x, y, iw, 1, bg)
		}
		kw := termwidth.Columns(it.key)
		p.putHL(x+1, y, it.label, strings.Fields(string(a.m.filter)), on(cText, bg), right-kw-4)
		if it.key != "" {
			p.Put(right-kw-2, y, " "+it.key+" ", boldOn(cText, cSurf2), right)
		}
		y++
	}
	if len(items) == 0 {
		p.Put(x+1, y, "No command by that name.", fgs(cFaint), right)
		y++
	}
	p.PutClip(x, y+1, "Every action by name. The key beside it does the same from the list.", fgs(cMuted), right)
}
