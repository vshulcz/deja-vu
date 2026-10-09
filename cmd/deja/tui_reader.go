package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
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
	hit    bool
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
}

const readerFold = 10

func (a *tuiApp) openReader() {
	s, ok := a.selected()
	if !ok {
		return
	}
	a.view = viewReader
	a.reader = readerState{s: s, terms: queryTerms(string(a.query))}
	a.want(s)
}

func roleColor(role string) tui.Color {
	switch role {
	case "user":
		return cAcc
	case "assistant":
		return cGrn
	}
	return cPeach
}

func (r *readerState) layout(width int, now func(model.Message) string) {
	r.layoutW = width
	r.lines, r.hits = nil, nil
	tw := width - 6
	if tw < 20 {
		tw = 20
	}
	for _, m := range r.d.full.Messages {
		text := digest.MessageText(m.Text)
		if text == "" {
			continue
		}
		var body []readerLine
		matched := false
		for _, raw := range strings.Split(text, "\n") {
			raw = strings.TrimRight(raw, " \t\r")
			wrapped := []string{raw}
			if !strings.HasPrefix(raw, "    ") && !strings.HasPrefix(raw, "\t") {
				wrapped = termwidth.Wrap(raw, tw)
			}
			for _, ln := range wrapped {
				hit := len(r.terms) > 0 && anyMarked(highlightMask([]rune(ln), r.terms))
				matched = matched || hit
				body = append(body, readerLine{text: ln, st: fgs(cText), indent: 4, hit: hit})
			}
		}
		if m.Role != "user" && m.Role != "assistant" {
			for i := range body {
				body[i].st = fgs(cSub)
			}
		}
		r.lines = append(r.lines, readerLine{text: m.Role, st: bold(roleColor(m.Role)), indent: 2, role: roleColor(m.Role)})
		if t := now(m); t != "" {
			r.lines[len(r.lines)-1].when = t
		}
		if !r.expand && !matched && len(body) > readerFold+2 {
			more := len(body) - readerFold
			body = append(body[:readerFold], readerLine{text: "… " + tuiCount(more, "more line") + " · t shows all", st: fgs(cFaint), indent: 4})
		}
		for _, b := range body {
			if b.hit {
				r.hits = append(r.hits, len(r.lines))
			}
			r.lines = append(r.lines, b)
		}
		r.lines = append(r.lines, readerLine{})
	}
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
	p.PutClip(x+2, 0, tuiProject(s)+" · "+tuiAgo(s.Updated, a.now)+" · "+search.ShortID(s.ID), on(cSub, cMantle), p.W-18)
	top, height := 2, p.H-3
	if r.d == nil {
		p.Put(4, top+1, "reading…", fgs(cFaint), p.W)
		a.drawFooter()
		return
	}
	if r.layoutW != p.W {
		r.layout(p.W, func(m model.Message) string {
			if m.Time.IsZero() {
				return ""
			}
			return m.Time.Local().Format("Jan 2 15:04")
		})
	}
	if !r.placed {
		r.placed = true
		if len(r.hits) > 0 {
			r.top = r.hits[0] - height/3
		}
	}
	r.clamp(height)
	if len(r.hits) > 0 {
		label := "hit " + num(r.hit+1) + " of " + num(len(r.hits))
		p.Put(p.W-termwidth.Columns(label)-2, 0, label, on(cMuted, cMantle), p.W)
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
		p.putHL(ln.indent, y, ln.text, r.terms, ln.st, p.W-2)
	}
	// Where the matches are in the whole session, down the right edge.
	if len(r.lines) > height {
		for _, h := range r.hits {
			y := top + h*height/len(r.lines)
			p.Put(p.W-1, y, "▐", fgs(cYel), p.W)
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
		a.ctrlKey(ev.Rune)
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
}
