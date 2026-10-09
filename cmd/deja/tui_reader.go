package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The reader opens a session at the first place it matched and walks the
// matches with n and N. Long messages fold to their first lines, except the
// ones that hold a match, so a hit is never hidden inside a fold.

type readerLine struct {
	text   string
	st     tui.Style
	indent int
	role   tui.Color // non-zero on a message's header line
	when   string    // beside the role, on a header line
	block  bool      // drawn on a block, like output in a terminal
	hit    bool
	turn   bool // the header of something the reader asked
}

type readerState struct {
	s       model.Session
	d       *tuiDetail
	terms   []string
	lines   []readerLine
	hits    []int
	hit     int
	top     int
	expand  bool
	layoutW int
	placed  bool

	turns   []int // the header line of each user turn
	turn    int   // the turn on screen, -1 above the first
	turnTop int   // the top the turn was worked out for
	finding bool  // the / line is open
	find    []rune
}

const (
	readerFold     = 10
	readerMaxLines = 400
)

func (a *tuiApp) openReader() {
	a.remember()
	s, ok := a.selected()
	if !ok {
		return
	}
	a.view = viewReader
	a.reader = readerState{s: s, terms: queryTerms(string(a.query))}
	a.want(s)
}

// readerRole is how one kind of message reads: its label, its colour, whether
// its body sits on a block like output does, and how many lines it shows
// before folding.
type readerRole struct {
	label string
	color tui.Color
	block bool
	fold  int
}

func roleLook(role, harness string) (readerRole, bool) {
	switch role {
	case "user":
		return readerRole{"you", cAcc, false, readerFold}, true
	case "assistant":
		return readerRole{agentName(harness), cGrn, false, readerFold}, true
	case "command":
		return readerRole{"ran", cPeach, true, 4}, true
	case "tool-output":
		return readerRole{"output", cSub, true, 6}, true
	case "edit":
		return readerRole{"edited", cYel, true, 8}, true
	case "files":
		return readerRole{"files", cMuted, false, 3}, true
	case "summary":
		return readerRole{"summary", cMuted, false, 4}, true
	case "wrote":
		// Hashes of written lines: an index for blame, not something to read.
		return readerRole{}, false
	}
	return readerRole{role, cPeach, false, readerFold}, true
}

func (r *readerState) layout(width int, now func(model.Message) string) {
	r.layoutW = width
	r.lines, r.hits, r.turns = nil, nil, nil
	r.turnTop = -1
	tw := width - 8
	if tw < 20 {
		tw = 20
	}
	for _, m := range r.d.full.Messages {
		look, ok := roleLook(m.Role, r.s.Harness)
		// As written, line breaks and all: the digest form joins a message into
		// one line and drops what looks like output, which is what a reader
		// opened the session to see.
		text := strings.Trim(redact.SafeForDisplay(strings.ReplaceAll(m.Text, "\t", "    ")), "\n ")
		if !ok || text == "" {
			continue
		}
		base := fgs(cText)
		if look.block || look.color == cMuted {
			base = fgs(cSub)
		}
		var body []readerLine
		matched := false
		for _, raw := range strings.Split(text, "\n") {
			raw = strings.TrimRight(raw, " \t\r")
			wrapped := []string{raw}
			if !look.block && !strings.HasPrefix(raw, "    ") && !strings.HasPrefix(raw, "\t") {
				wrapped = termwidth.Wrap(raw, tw)
			}
			for _, ln := range wrapped {
				hit := len(r.terms) > 0 && anyMarked(highlightMask([]rune(ln), r.terms))
				matched = matched || hit
				body = append(body, readerLine{text: ln, st: base, indent: 4, hit: hit, block: look.block})
			}
		}
		head := readerLine{text: look.label, st: bold(look.color), indent: 2, role: look.color, when: now(m), turn: m.Role == "user"}
		if head.turn {
			r.turns = append(r.turns, len(r.lines))
		}
		r.lines = append(r.lines, head)
		fold := look.fold
		if r.expand || matched {
			// Unfolded still has an end: one test log can run to thousands
			// of lines, and the screen is for reading, not for the log.
			fold = readerMaxLines
		}
		if len(body) > fold+1 {
			more := len(body) - fold
			note := " · t shows all"
			if fold == readerMaxLines {
				note = " · deja show has the whole session"
			}
			body = append(body[:fold], readerLine{text: "… " + tuiCount(more, "more line") + note, st: fgs(cFaint), indent: 4, block: look.block})
		}
		for _, b := range body {
			if b.hit {
				r.hits = append(r.hits, len(r.lines))
			}
			r.lines = append(r.lines, b)
		}
		r.lines = append(r.lines, readerLine{})
	}
	// A new width wraps the hits into a different count.
	if r.hit >= len(r.hits) {
		r.hit = max(0, len(r.hits)-1)
	}
}

func readerWhen(m model.Message) string {
	if m.Time.IsZero() {
		return ""
	}
	return m.Time.Local().Format("Jan 2 15:04")
}

func anyMarked(m []bool) bool {
	for _, b := range m {
		if b {
			return true
		}
	}
	return false
}

func (a *tuiApp) drawReader() {
	p := a.p
	r := &a.reader
	if r.d == nil {
		r.d = a.details[sessionKey(r.s)]
	}
	s := r.s
	p.Fill(0, 0, p.W, 1, cMantle)
	x := p.Put(1, 0, "◆ deja", boldOn(cAcc, cMantle), p.W)
	x = p.Put(x+3, 0, "●", on(agentColor(s.Harness), cMantle), p.W)
	x = p.Put(x+1, 0, agentName(s.Harness), boldOn(cText, cMantle), p.W)
	where := r.position()
	p.PutClip(x+2, 0, tuiProject(s)+" · "+tuiAgo(s.Updated, a.now)+" · "+search.ShortID(s.ID), on(cSub, cMantle), p.W-termwidth.Columns(where)-5)
	top, height := 2, p.H-3
	if r.d == nil {
		for b := 0; b < 3 && top+1+b*5 < p.H-2; b++ {
			a.drawSkeleton(4, top+1+b*5, min(p.W-8, 90), min(3, p.H-3-(top+1+b*5)))
		}
		a.drawFooter()
		return
	}
	if r.layoutW != p.W {
		r.layout(p.W, readerWhen)
	}
	if !r.placed {
		r.placed = true
		if len(r.hits) > 0 {
			r.jump(0, height)
		}
	}
	r.clamp(height)
	r.syncTurn(height)
	if where = r.position(); where != "" {
		p.Put(p.W-termwidth.Columns(where)-2, 0, where, on(cMuted, cMantle), p.W)
	}
	for i := 0; i < height; i++ {
		n := r.top + i
		if n >= len(r.lines) {
			break
		}
		ln := r.lines[n]
		y := top + i
		if ln.role != 0 {
			p.Put(ln.indent, y, "▍", fgs(ln.role), p.W)
			x := p.Put(ln.indent+2, y, ln.text, ln.st, p.W-2)
			p.Put(x+2, y, ln.when, fgs(cMuted), p.W-2)
			continue
		}
		if ln.block {
			p.Fill(ln.indent, y, p.W-ln.indent-3, 1, cMantle)
			p.putHL(ln.indent+1, y, ln.text, r.terms, ln.st, p.W-4)
			continue
		}
		p.putHL(ln.indent, y, ln.text, r.terms, ln.st, p.W-2)
	}
	// Where the matches are in the whole session, down the right edge.
	if len(r.lines) > height {
		for _, h := range r.hits {
			y := top + h*height/len(r.lines)
			p.Put(p.W-1, y, "▐", fgs(cMark), p.W)
		}
	}
	a.drawFooter()
}

func (r *readerState) clamp(height int) {
	if r.top > len(r.lines)-height {
		r.top = len(r.lines) - height
	}
	if r.top < 0 {
		r.top = 0
	}
}

func (a *tuiApp) handleReader(ev tui.Event) {
	r := &a.reader
	page := a.p.H - 4
	if r.finding {
		a.handleFind(ev)
		return
	}
	if ev.Kind == tui.EvMouse {
		switch ev.Button {
		case tui.MouseWheelUp:
			r.top -= 3
		case tui.MouseWheelDown:
			r.top += 3
		}
		return
	}
	if ev.Kind != tui.EvKey {
		return
	}
	switch ev.Key {
	case tui.KeyUp:
		r.top--
	case tui.KeyDown:
		r.top++
	case tui.KeyPgUp:
		r.top -= page
	case tui.KeyPgDn:
		r.top += page
	case tui.KeyHome:
		r.top = 0
	case tui.KeyEnd:
		r.top = len(r.lines)
	case tui.KeyEsc, tui.KeyBackspace:
		a.view = viewList
	case tui.KeyCtrl:
		// The list's own keys (^u ^w ^n ^p) would change the hidden list
		// under an open session; here they move through it the way less does.
		switch ev.Rune {
		case 'n':
			r.top++
		case 'p':
			r.top--
		case 'd':
			r.top += page / 2
		case 'u':
			r.top -= page / 2
		case 'r', 'o', 'y', 'k':
			a.ctrlKey(ev.Rune)
		}
	case tui.KeyRune:
		switch ev.Rune {
		case 'k':
			r.top--
		case 'j':
			r.top++
		case ' ':
			r.top += page
		case 'b':
			r.top -= page
		case 'g':
			r.top = 0
		case 'G':
			r.top = len(r.lines)
		case 'n':
			r.jump(1, a.p.H-3)
		case 'N':
			r.jump(-1, a.p.H-3)
		case '/':
			r.finding, r.find = true, nil
		case ']':
			r.jumpTurn(1, a.p.H-3)
		case '[':
			r.jumpTurn(-1, a.p.H-3)
		case 't':
			r.expand = !r.expand
			r.layoutW = 0
		case 'q':
			a.view = viewList
		case 'r':
			a.resumeSelected()
		case 'R':
			a.putBack()
		case 'o':
			a.openContinue()
		case 'c':
			a.copyContext()
		case '?':
			a.openModal(modalHelp)
		}
	}
}

func (r *readerState) jump(d, height int) {
	if len(r.hits) == 0 {
		return
	}
	r.hit = (r.hit + d + len(r.hits)) % len(r.hits)
	r.top = r.hits[r.hit] - height/3
	r.clamp(height)
	r.pinTurn(r.hits[r.hit])
}
