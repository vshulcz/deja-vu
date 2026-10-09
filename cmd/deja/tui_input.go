package main

import (
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/tui"
)

// Two kinds of key share the keyboard. While the cursor is in the search box
// every letter types; once the reader moves into the list with an arrow, the
// letters are actions, and any letter that is not one goes back to the box.
// The footer always says which of the two is live.

func (a *tuiApp) handle(ev tui.Event) {
	if ev.Kind == tui.EvResize {
		if a.t != nil {
			a.t.Invalidate()
		}
		if a.view == viewReader {
			a.reader.layoutW = 0
		}
		return
	}
	if ev.Kind == tui.EvKey && ev.Key == tui.KeyCtrl && ev.Rune == 'c' {
		a.quit = true
		return
	}
	switch {
	case a.modal != modalNone:
		a.handleModal(ev)
	case a.view == viewReader:
		a.handleReader(ev)
	case a.view == viewWelcome:
		a.handleWelcome(ev)
	default:
		a.handleList(ev)
	}
}

func (a *tuiApp) handleList(ev tui.Event) {
	switch ev.Kind {
	case tui.EvPaste:
		text := strings.Join(strings.Fields(ev.Text), " ")
		a.query = append(a.query, []rune(text)...)
		a.listFocus = false
		a.reload()
		return
	case tui.EvMouse:
		a.clickList(ev)
		return
	}
	if ev.Key != tui.KeyUp {
		a.histAt = -1
	}
	switch ev.Key {
	case tui.KeyUp:
		// At the top of the box, ↑ walks back through past searches, the
		// way a shell does.
		if !a.listFocus && a.sel == 0 && (len(a.query) == 0 || a.histAt >= 0) && len(a.history) > 0 {
			a.recall(-1)
			return
		}
		a.move(-1)
		a.listFocus = true
	case tui.KeyDown:
		// Down goes into the list even from a recalled search: the results
		// are what the reader came back for.
		a.move(1)
		a.listFocus = true
	case tui.KeyPgUp:
		a.move(-5)
	case tui.KeyPgDn:
		a.move(5)
	case tui.KeyHome:
		a.move(-len(a.rows))
	case tui.KeyEnd:
		a.move(len(a.rows))
	case tui.KeyTab:
		a.setScope((a.scope + 1) % 3)
	case tui.KeyBackTab:
		a.setScope((a.scope + 2) % 3)
	case tui.KeyEnter:
		a.openReader()
	case tui.KeyEsc:
		switch {
		case a.listFocus:
			a.listFocus = false
		case len(a.query) > 0:
			a.query = nil
			a.reload()
		default:
			a.quit = true
		}
	case tui.KeyBackspace:
		a.listFocus = false
		if n := len(a.query); n > 0 {
			a.query = a.query[:n-1]
			a.reload()
		}
	case tui.KeyCtrl:
		a.ctrlKey(ev.Rune)
	case tui.KeyRune:
		if ev.Alt {
			return
		}
		if a.listFocus && a.listAction(ev.Rune) {
			return
		}
		// The footer offers ? for help on the empty box, and a lone ? is
		// not a search anybody means.
		if ev.Rune == '?' && len(a.query) == 0 {
			a.openModal(modalHelp)
			return
		}
		a.listFocus = false
		a.query = append(a.query, ev.Rune)
		a.reload()
	}
}

func (a *tuiApp) ctrlKey(r rune) {
	switch r {
	case 'u':
		a.query = nil
		a.reload()
	case 'w':
		q := strings.TrimRight(string(a.query), " ")
		if i := strings.LastIndex(q, " "); i >= 0 {
			a.query = []rune(q[:i+1])
		} else {
			a.query = nil
		}
		a.reload()
	case 'r':
		a.resumeSelected()
	case 'o':
		a.openContinue()
	case 'y':
		a.copyContext()
	case 'k':
		a.openPalette()
	case 'n':
		a.move(1)
	case 'p':
		a.move(-1)
	}
}

// listAction runs a letter as an action and reports whether it was one.
func (a *tuiApp) listAction(r rune) bool {
	if r >= '1' && r <= '9' && len(a.rows) > 0 {
		// The nth card on screen, so the number matches what the eye counts.
		a.sel = min(a.firstShown+int(r-'1'), len(a.rows)-1)
		a.want(a.rows[a.sel].s)
		return true
	}
	switch r {
	case 'j':
		a.move(1)
	case 'k':
		a.move(-1)
	case 'r':
		a.resumeSelected()
	case 'R':
		a.putBack()
	case 'o':
		a.openContinue()
	case 'c':
		a.copyContext()
	case 'a':
		a.openModal(modalAgents)
	case '?':
		a.openModal(modalHelp)
	case 'q':
		a.quit = true
	case '/':
		a.listFocus = false
	default:
		return false
	}
	return true
}

func (a *tuiApp) move(d int) {
	if len(a.rows) == 0 {
		return
	}
	a.sel += d
	if a.sel < 0 {
		a.sel = 0
	}
	if a.sel >= len(a.rows) {
		a.sel = len(a.rows) - 1
	}
	a.want(a.rows[a.sel].s)
}

func (a *tuiApp) setScope(s int) {
	if s == a.scope {
		return
	}
	a.scope = s
	if s == scopeKept && !a.keptLoaded {
		a.say("Checking which sessions their agents deleted…", true)
	}
	a.reload()
}

// clickList selects a card on a click, opens it on a click on the selected
// one, and scrolls on the wheel.
func (a *tuiApp) clickList(ev tui.Event) {
	switch ev.Button {
	case tui.MouseWheelUp:
		a.move(-1)
		return
	case tui.MouseWheelDown:
		a.move(1)
		return
	}
	if !ev.Press || ev.Button != tui.MouseLeft {
		return
	}
	for _, z := range a.zones {
		if ev.X >= z.x0 && ev.X < z.x1 && ev.Y >= z.y0 && ev.Y < z.y1 {
			z.do()
			return
		}
	}
}

// zone is a clickable rectangle the last frame drew.
type zone struct {
	x0, y0, x1, y1 int
	do             func()
}

func (a *tuiApp) addZone(x0, y0, x1, y1 int, do func()) {
	a.zones = append(a.zones, zone{x0, y0, x1, y1, do})
}

func (a *tuiApp) clickCard(i int) {
	if i == a.sel && time.Since(a.lastClick) < 600*time.Millisecond {
		a.openReader()
		return
	}
	a.sel, a.listFocus, a.lastClick = i, true, time.Now()
	a.want(a.rows[i].s)
}
