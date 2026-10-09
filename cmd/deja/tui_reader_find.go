package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// Inside an open session, / looks for words the list search did not, and
// ] and [ step from one thing the reader asked to the next, which is how a
// long session is usually skimmed.

// position is what the top bar says about where the reader is.
func (r *readerState) position() string {
	var parts []string
	if len(r.turns) > 0 {
		parts = append(parts, "turn "+num(max(r.turn, 0)+1)+" of "+num(len(r.turns)))
	}
	if len(r.hits) > 0 {
		parts = append(parts, "hit "+num(r.hit+1)+" of "+num(len(r.hits)))
	}
	return strings.Join(parts, " · ")
}

// syncTurn works the turn out again once the reader scrolled away from where
// a jump left them: the turn whose header is at or above the top line, or the
// last one once the session is scrolled to its end.
func (r *readerState) syncTurn(height int) {
	if r.top == r.turnTop {
		return
	}
	line := r.top
	if r.top > 0 && r.top+height >= len(r.lines) {
		line = len(r.lines)
	}
	r.pinTurn(line)
}

// pinTurn makes the turn holding line the current one for this top. A jump
// pins the turn it went to, which is not always the one at the top line: a
// match sits a third of the way down, and the last turns cannot scroll up.
func (r *readerState) pinTurn(line int) {
	r.turn, r.turnTop = -1, r.top
	for i, ln := range r.turns {
		if ln > line {
			break
		}
		r.turn = i
	}
}

// jumpTurn moves to the next (d > 0) or previous turn. Back from the middle of
// a turn goes to its start first, the way [ does in an editor.
func (r *readerState) jumpTurn(d, height int) {
	if len(r.turns) == 0 {
		return
	}
	r.syncTurn(height)
	k := r.turn + 1
	if d < 0 {
		k = r.turn - 1
		if r.turn >= 0 && r.top > r.turns[r.turn] {
			k = r.turn
		}
	}
	if k < 0 || k >= len(r.turns) {
		return
	}
	r.top = r.turns[k]
	r.clamp(height)
	r.pinTurn(r.turns[k])
}

func (a *tuiApp) handleFind(ev tui.Event) {
	r := &a.reader
	if ev.Kind == tui.EvPaste {
		r.find = append(r.find, []rune(strings.Join(strings.Fields(ev.Text), " "))...)
		return
	}
	if ev.Kind != tui.EvKey {
		return
	}
	switch ev.Key {
	case tui.KeyEsc:
		r.finding = false
	case tui.KeyEnter:
		a.runFind()
	case tui.KeyBackspace:
		if n := len(r.find); n > 0 {
			r.find = r.find[:n-1]
		}
	case tui.KeyCtrl:
		if ev.Rune == 'u' {
			r.find = nil
		}
	case tui.KeyRune:
		if !ev.Alt {
			r.find = append(r.find, ev.Rune)
		}
	}
}

// runFind makes the typed words the session's matches and goes to the first
// one; a word that is not there leaves the matches as they were.
func (a *tuiApp) runFind() {
	r := &a.reader
	q := strings.TrimSpace(string(r.find))
	r.finding = false
	if q == "" {
		return
	}
	terms := queryTerms(q)
	if len(terms) == 0 {
		a.say("Two letters or more.", false)
		return
	}
	if r.d == nil || r.layoutW == 0 {
		// Not laid out yet: the first frame places it at the first match.
		r.terms, r.hit, r.placed = terms, 0, false
		return
	}
	old := r.terms
	r.terms = terms
	r.layout(r.layoutW, readerWhen)
	if len(r.hits) == 0 {
		r.terms = old
		r.layout(r.layoutW, readerWhen)
		a.say("No “"+q+"” in this session.", false)
		return
	}
	r.hit, r.placed = 0, true
	r.jump(0, a.p.H-3)
}

// drawFind is the / line, in the footer's place while it is open.
func (a *tuiApp) drawFind() {
	p := a.p
	y := p.H - 1
	p.Fill(0, y, p.W, 1, cSurf)
	hint := [][2]string{{"↵", "find"}, {"esc", "close"}}
	hw := 0
	for _, k := range hint {
		hw += termwidth.Columns(k[0]) + termwidth.Columns(k[1]) + 6
	}
	right := p.W - 2
	if p.W-hw > 30 {
		right = p.W - hw - 1
		x := right + 1
		for _, k := range hint {
			x = p.keycap(x, y, k[0], k[1], p.W)
		}
	}
	x := p.Put(2, y, "/", boldOn(cAcc, cSurf), right)
	q := string(a.reader.find)
	if termwidth.Columns(q) > right-x-3 {
		q = "…" + termwidth.CutRight(q, right-x-4)
	}
	x = p.Put(x+1, y, q, boldOn(cText, cSurf), right)
	if len(a.reader.find) == 0 {
		p.Put(x, y, "▏", on(cAcc, cSurf), right)
		p.PutClip(x+1, y, "find in this session", on(cMuted, cSurf), right)
		return
	}
	p.Put(x, y, "▏", on(cAcc, cSurf), right+1)
}
