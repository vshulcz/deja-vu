package main

import (
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The memory page is what an agent starting in this project reads first:
// the decisions promoted in deja, and what Claude Code wrote down for itself
// in its auto-memory. Claude reads its own memory and nothing else does, so
// the page is also where a person sees what the other agents are handed.

const (
	memStanding = iota
	memLearned
	memAside
)

// memItem is one line on the page and where it came from.
type memItem struct {
	kind   int
	text   string
	kindOf string // a memory's own type: feedback, project, user, reference
	body   string
	path   string        // a memory file; "" for a promoted decision
	src    model.Session // the session behind it, when the index still has it
	at     time.Time
}

type memoryState struct {
	items   []memItem
	sel     int
	scroll  int
	loaded  bool
	learned int // Claude memories handed on
}

func (a *tuiApp) openMemory() {
	a.view = viewMemory
	if !a.mem.loaded {
		dir, projects, cwd := a.dir, a.projects, a.cwd
		a.spawn(func() {
			m := loadMemory(dir, projects, cwd)
			a.post(func() {
				m.sel = min(a.mem.sel, max(len(m.items)-1, 0))
				a.mem = m
			})
		})
	}
}

func loadMemory(dir string, projects []string, cwd string) memoryState {
	m := memoryState{loaded: true}
	for _, n := range sources.LoadPromotedNotes() {
		if !inAnyProject(n.Project, projects) {
			continue
		}
		it := memItem{text: conventionLine(n), at: n.At, kind: memAside}
		if n.State == "accepted" {
			it.kind = memStanding
		}
		if h, id, ok := strings.Cut(n.Session, ":"); ok {
			if s, found, _ := index.FindByIdentity(dir, h, id); found {
				it.src = s
			}
		}
		m.items = append(m.items, it)
	}
	hidden := memHidden(dir)
	var mems []sources.AgentMemory
	if memoryAllowed(cwd, policy.ActivationSearch) {
		mems = projectClaudeMemories(cwd)
	}
	for _, cm := range mems {
		it := memItem{kind: memLearned, text: cm.Description, kindOf: memTypeName(cm.Type), body: plainMarkdown(redactedText(cm.Body)), path: cm.Path, at: cm.Modified}
		if it.text == "" {
			it.text = firstLine(cm.Body)
		}
		if hidden[cm.Path] {
			it.kind = memAside
		} else {
			m.learned++
		}
		if cm.Origin != "" {
			if s, found, _ := index.FindByIdentity(dir, "claude", cm.Origin); found {
				it.src = s
			}
		}
		m.items = append(m.items, it)
	}
	// Rules first: how to work here is what another agent gets wrong without.
	rank := map[string]int{"rule": 0, "about you": 1, "fact": 2, "pointer": 3}
	sort.SliceStable(m.items, func(i, j int) bool {
		a, b := m.items[i], m.items[j]
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if ra, rb := rank[a.kindOf], rank[b.kindOf]; ra != rb {
			return ra < rb
		}
		return a.at.After(b.at)
	})
	return m
}

func redactedText(s string) string {
	out, _ := redact.Text(s)
	return out
}

// memTypeName says Claude's memory types the way a person would: feedback
// is a rule for how to work, project a fact about the work.
func memTypeName(t string) string {
	switch t {
	case "feedback":
		return "rule"
	case "project":
		return "fact"
	case "reference":
		return "pointer"
	case "user":
		return "about you"
	}
	return t
}

// plainMarkdown drops the emphasis markers a memory body is written with;
// the preview has no bold to give them.
func plainMarkdown(s string) string {
	return strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
}

// memHiddenPath lists the memory files a person told deja not to hand on.
// Claude's own file is left alone: it is Claude's, and Claude still reads it.
func memHiddenPath(dir string) string { return dir + ".memhidden" }

func memHidden(dir string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(memHiddenPath(dir))
	if err != nil {
		return out
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out[ln] = true
		}
	}
	return out
}

func setMemHidden(dir, path string, hide bool) error {
	h := memHidden(dir)
	if hide {
		h[path] = true
	} else {
		delete(h, path)
	}
	var lines []string
	for p := range h {
		lines = append(lines, p)
	}
	sort.Strings(lines)
	return atomicfile.Write(memHiddenPath(dir), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func (a *tuiApp) memSelected() (memItem, bool) {
	if a.mem.sel < 0 || a.mem.sel >= len(a.mem.items) {
		return memItem{}, false
	}
	return a.mem.items[a.mem.sel], true
}

// memKeep and memDrop are y and x: a memory is handed on or not, a promoted
// decision is marked through `deja promote`, so the page writes exactly what
// the command would.
func (a *tuiApp) memKeep() { a.memMark(false) }
func (a *tuiApp) memDrop() { a.memMark(true) }

func (a *tuiApp) memMark(drop bool) {
	it, ok := a.memSelected()
	if !ok {
		return
	}
	var err error
	switch {
	case it.path != "":
		err = setMemHidden(a.dir, it.path, drop)
	case it.src.ID == "":
		return
	default:
		args := []string{it.src.ID, "--note", it.text}
		if drop {
			args = append(args, "--state", "stale")
		}
		err = runPromote(a.dir, args, io.Discard)
	}
	if err != nil {
		a.say(err.Error(), false)
		return
	}
	cur := &a.mem.items[a.mem.sel]
	switch {
	case drop:
		if cur.kind == memLearned {
			a.mem.learned--
		}
		cur.kind = memAside
		a.say("No agent is handed this any more.", true)
	case it.path != "":
		if cur.kind == memAside {
			a.mem.learned++
		}
		cur.kind = memLearned
		a.say("Every agent here gets this again.", true)
	default:
		cur.kind = memStanding
		a.say("Every new session here starts with this.", true)
	}
}

func (a *tuiApp) memOpenSource() {
	it, ok := a.memSelected()
	if !ok || it.src.ID == "" {
		return
	}
	a.view = viewReader
	a.readerBack = viewMemory
	a.reader = readerState{s: it.src}
	a.want(it.src)
}

func (a *tuiApp) handleMemory(ev tui.Event) {
	if ev.Kind == tui.EvMouse {
		switch ev.Button {
		case tui.MouseWheelUp:
			a.mem.sel = max(a.mem.sel-1, 0)
		case tui.MouseWheelDown:
			a.mem.sel = min(a.mem.sel+1, max(len(a.mem.items)-1, 0))
		default:
			a.clickList(ev)
		}
		return
	}
	if ev.Kind != tui.EvKey {
		return
	}
	n := len(a.mem.items)
	switch ev.Key {
	case tui.KeyUp:
		a.mem.sel = max(a.mem.sel-1, 0)
	case tui.KeyDown:
		a.mem.sel = min(a.mem.sel+1, max(n-1, 0))
	case tui.KeyTab:
		a.view = viewList
		a.setScope(scopeHere)
	case tui.KeyBackTab:
		a.view = viewList
		a.setScope(scopeKept)
	case tui.KeyEnter:
		a.memOpenSource()
	case tui.KeyEsc:
		a.view = viewList
	case tui.KeyRune:
		switch ev.Rune {
		case 'j':
			a.mem.sel = min(a.mem.sel+1, max(n-1, 0))
		case 'k':
			a.mem.sel = max(a.mem.sel-1, 0)
		case 'y':
			a.memKeep()
		case 'x':
			a.memDrop()
		case 'q':
			a.quit = true
		case '?':
			a.openModal(modalHelp)
		default:
			// Anything else is the start of a search, as on every other tab.
			a.view = viewList
			a.query = append(a.query[:0], ev.Rune)
			a.reload()
		}
	}
}

// memOthers names the agents other than Claude that work in this project,
// most sessions first: the ones the page's memory is handed to.
func (a *tuiApp) memOthers() []string {
	var out []string
	for _, c := range a.agentsHere {
		if c.h != "claude" {
			out = append(out, agentName(c.h))
		}
	}
	return out
}

func (a *tuiApp) drawMemory() {
	l := newLayout(a.p.W, a.p.H)
	p := a.p
	a.drawTop(l)
	name := "this project"
	if len(a.projects) > 0 {
		name = a.projects[0]
	}
	x := p.Put(l.listX+1, 2, "What every agent starting in ", fgs(cSub), l.w-1)
	x = p.Put(x, 2, name, bold(cText), l.w-1)
	p.Put(x, 2, " reads first", fgs(cSub), l.w-1)
	if !a.mem.loaded {
		p.Put(l.listX+1, l.bodyTop, "Reading what this project's agents remember…", fgs(cMuted), l.listX+l.listW)
		a.drawFooter()
		return
	}
	if a.mem.learned > 0 && l.bodyTop == 6 {
		x := p.Put(l.listX+1, 4, "●", fgs(agentColor("claude")), l.w-1)
		x = p.Put(x+1, 4, "Claude Code", bold(cText), l.w-1)
		x = p.Put(x, 4, " wrote down "+tuiCount(a.mem.learned, "thing")+" here", fgs(cSub), l.w-1)
		if others := a.memOthers(); len(others) > 0 {
			if len(others) > 3 {
				others = append(others[:3], "the rest")
			}
			x = p.Put(x, 4, " · deja hands them to ", fgs(cSub), l.w-1)
			p.PutClip(x, 4, strings.Join(others, ", "), bold(cText), l.w-1)
		}
	}
	a.drawMemList(l)
	if l.prevW > 0 {
		a.drawMemPreview(l)
	}
	a.drawFooter()
}

var memHeads = [...]struct{ label, note string }{
	memStanding: {"DECIDED", "promoted in deja"},
	memLearned:  {"LEARNED BY CLAUDE CODE", "its own memory, handed to every agent"},
	memAside:    {"NOT HANDED ON", "y puts one back"},
}

func memMark(kind int) (string, tui.Color) {
	switch kind {
	case memStanding:
		return "●", cGrn
	case memLearned:
		return "◆", agentColor("claude")
	}
	return "·", cFaint
}

func (a *tuiApp) drawMemList(l layout) {
	p := a.p
	x0, x1 := l.listX, l.listX+l.listW
	top, bottom := l.bodyTop, l.bodyTop+l.bodyH
	if len(a.mem.items) == 0 {
		p.Put(x0+1, top, "Nothing decided or remembered here yet.", fgs(cMuted), x1)
		return
	}
	// One heading per kind, then two lines per item and a gap.
	type row struct{ item, kind, line int } // item -1: heading, -2: blank
	var rows []row
	last := -1
	for i, it := range a.mem.items {
		if it.kind != last {
			if last >= 0 {
				rows = append(rows, row{-2, 0, 0})
			}
			rows = append(rows, row{-1, it.kind, 0})
			last = it.kind
		}
		rows = append(rows, row{i, it.kind, 0}, row{i, it.kind, 1})
	}
	selAt := 0
	for i, r := range rows {
		if r.item == a.mem.sel {
			selAt = i
			break
		}
	}
	h := bottom - top
	if selAt-1 < a.mem.scroll {
		a.mem.scroll = max(selAt-1, 0)
	}
	if selAt+2 > a.mem.scroll+h {
		a.mem.scroll = selAt + 2 - h
	}
	counts := map[int]int{}
	for _, it := range a.mem.items {
		counts[it.kind]++
	}
	for i := a.mem.scroll; i < len(rows) && top+i-a.mem.scroll < bottom; i++ {
		y := top + i - a.mem.scroll
		r := rows[i]
		if r.item == -1 {
			hd := memHeads[r.kind]
			x := p.Put(x0+1, y, hd.label, fgs(cMuted), x1)
			x = p.Put(x+2, y, num(counts[r.kind]), fgs(cFaint), x1)
			p.PutClip(x+3, y, hd.note, fgs(cFaint), x1)
			continue
		}
		if r.item < 0 {
			continue
		}
		it := a.mem.items[r.item]
		sel := r.item == a.mem.sel
		surf := cBase
		if sel {
			surf = cSurf
		}
		p.Fill(x0+1, y, l.listW-1, 1, surf)
		mark, mc := memMark(it.kind)
		if sel {
			p.Put(x0, y, "▌", fgs(mc), x1)
		}
		if r.line == 0 {
			p.Put(x0+2, y, mark, on(mc, surf), x1)
			st := boldOn(cText, surf)
			if it.kind == memAside {
				st = on(cMuted, surf)
			}
			p.PutClip(x0+4, y, it.text, st, x1-1)
			idx := r.item
			a.addZone(x0, y, x1, y+2, func() { a.mem.sel = idx })
			continue
		}
		var meta []string
		if it.kindOf != "" {
			meta = append(meta, it.kindOf)
		}
		if !it.at.IsZero() {
			meta = append(meta, tuiAgo(it.at, a.now))
		}
		if it.kind == memStanding && it.src.ID != "" {
			meta = append(meta, a.headline(it.src))
		}
		mx := x0 + 4
		if len(meta) > 0 && it.kindOf == "rule" {
			mx = p.Put(mx, y, meta[0], on(cAcc, surf), x1-1)
			meta = meta[1:]
			if len(meta) > 0 {
				mx = p.Put(mx, y, " · ", on(cMuted, surf), x1-1)
			}
		}
		p.PutClip(mx, y, strings.Join(meta, " · "), on(cMuted, surf), x1-1)
	}
}

func (a *tuiApp) drawMemPreview(l layout) {
	it, ok := a.memSelected()
	if !ok {
		return
	}
	p := a.p
	x, y, w, h := l.prevX, l.bodyTop, l.prevW, l.bodyH
	p.box(x, y, w, h, cSurf)
	ix, iw := x+2, w-4
	right := ix + iw
	bottom := y + h - 3
	cy := y + 1
	mark, mc := memMark(it.kind)
	label := map[int]string{memStanding: "Decided", memLearned: "Claude Code memory", memAside: "Not handed on"}[it.kind]
	cx := p.Put(ix, cy, mark+" "+label, bold(mc), right)
	if it.kindOf != "" {
		p.Put(cx+2, cy, it.kindOf, fgs(cMuted), right)
	}
	cy += 2
	for _, ln := range wrapLines(it.text, iw-2, 4) {
		p.Put(ix, cy, "▎", fgs(mc), right)
		p.Put(ix+2, cy, ln, bold(cText), right)
		cy++
	}
	cy++
	if it.body != "" && cy < bottom-7 {
		room := bottom - 7 - cy
		if it.src.ID == "" {
			room += 5
		}
		for _, ln := range wrapLines(it.body, iw, max(room, 1)) {
			p.Put(ix, cy, ln, fgs(cSub), right)
			cy++
		}
		cy++
	}
	if it.src.ID != "" && cy < bottom-4 {
		p.Put(ix, cy, "WRITTEN IN", fgs(cMuted), right)
		cy++
		cx := p.Put(ix, cy, "●", fgs(agentColor(it.src.Harness)), right)
		cx = p.Put(cx+1, cy, agentName(it.src.Harness), bold(cText), right)
		p.PutClip(cx+2, cy, tuiAgo(it.src.Updated, a.now), fgs(cMuted), right)
		cy++
		for _, ln := range wrapLines(a.headline(it.src), iw, min(2, bottom-1-cy)) {
			p.Put(ix, cy, ln, fgs(cSub), right)
			cy++
		}
	}
	type act struct {
		k, label string
		do       func()
	}
	var acts []act
	switch it.kind {
	case memAside:
		acts = append(acts, act{"y", "Hand on again", a.memKeep})
	default:
		acts = append(acts, act{"x", "Don't hand on", a.memDrop})
	}
	if it.src.ID != "" {
		acts = append([]act{{"↵", "Open session", a.memOpenSource}}, acts...)
	}
	bx := ix
	for i, ac := range acts {
		if termwidth.Columns(ac.label)+4 > right-bx {
			break
		}
		x0 := bx
		bx = p.button(bx, bottom, ac.k, ac.label, i == 0, right)
		a.addZone(x0, bottom, bx, bottom+1, ac.do)
		bx += 2
	}
}
