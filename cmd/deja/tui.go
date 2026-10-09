package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The interactive screen a bare `deja` opens at a terminal: search as you
// type across every agent, read a session at the hit, and resume it or carry
// it into another agent. Everything it shows comes from the same index and
// the same functions the commands use; it only adds the screen.

const (
	scopeHere = iota
	scopeAll
	scopeKept
)

const (
	viewList = iota
	viewReader
	viewWelcome
)

const (
	modalNone = iota
	modalContinue
	modalAgents
	modalHelp
	modalPalette
)

type tuiApp struct {
	dir   string
	t     *tui.Term
	p     painter
	now   time.Time
	clock func() time.Time   // nil: the wall clock; tests pin it
	copy  func(string) error // nil: the clipboard; tests catch it

	scope       int
	projects    []string
	cwd         string
	behind      []behindRow
	allMeta     []model.Session
	agentsAll   []agentCount
	filter      map[string]bool
	query       []rune
	listFocus   bool
	rows        []tuiRow
	listed      string // the scope and query rows answers
	total       int
	sel, scroll int
	tookMS      float64
	widened     bool
	searching   bool
	seq         int

	kept       []model.Session
	keptLoaded bool
	keptIDs    map[string]bool

	details map[string]*tuiDetail
	loading map[string]bool
	loadQ   chan model.Session
	updates chan func()

	view   int
	reader readerState
	modal  int
	m      modalState

	toast      string
	toastGood  bool
	toastUntil time.Time
	indexing   bool

	zones     []zone
	lastClick time.Time
	welcome   welcomeState

	history    []string // past searches, oldest first
	histAt     int      // where ↑ is in history, -1 when not browsing
	firstShown int      // the first card on screen, for 1-9

	after func() error
	quit  bool
}

// tuiWanted says whether a bare deja should open the screen: a person at a
// terminal on both ends, and no request to keep to text.
func tuiWanted() bool {
	if os.Getenv("DEJA_TUI") == "0" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return briefWanted(os.Stdout) && briefWanted(os.Stdin)
}

// runTUI opens the screen and, once it closes, runs whatever the reader
// picked on the way out (a resume, a handoff), with the terminal theirs again.
func runTUI(dir string) error {
	first := !index.HasManifest(dir)
	t, err := tui.Open()
	if err != nil {
		return runBrief(dir, os.Stdout)
	}
	a := newTUIApp(dir, t)
	if first {
		a.view = viewWelcome
	}
	// Anything the index writes to stderr while the screen is up would land
	// in the middle of a frame.
	stderr := os.Stderr
	if null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		os.Stderr = null
		defer null.Close()
	}
	func() {
		defer func() {
			os.Stderr = stderr
			t.Close()
			if r := recover(); r != nil {
				panic(r)
			}
		}()
		a.run()
	}()
	if a.after != nil {
		return a.after()
	}
	return nil
}

func newTUIApp(dir string, t *tui.Term) *tuiApp {
	a := &tuiApp{
		dir:     dir,
		t:       t,
		details: map[string]*tuiDetail{},
		loading: map[string]bool{},
		loadQ:   make(chan model.Session, 512),
		updates: make(chan func(), 64),
		filter:  map[string]bool{},
		keptIDs: map[string]bool{},
	}
	a.p.mono = t != nil && t.Mode == tui.ModeMono
	setTheme(lightTerminal())
	a.cwd = howCwd()
	a.projects = howScope(a.cwd, "", false)
	a.history, a.histAt = loadTUIHistory(dir), -1
	return a
}

func (a *tuiApp) post(f func()) {
	select {
	case a.updates <- f:
	default:
		go func() { a.updates <- f }()
	}
}

func (a *tuiApp) run() {
	a.loadHome()
	if a.scope == scopeHere && a.total == 0 {
		a.scope = scopeAll
		a.loadHome()
	}
	go a.detailWorker()
	if a.view == viewWelcome {
		go a.firstBuild()
	} else {
		go a.refreshIndex()
		go a.loadKept()
		go a.loadBehind()
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for !a.quit {
		a.render()
		select {
		case ev := <-a.t.Events():
			a.handle(ev)
		case f := <-a.updates:
			f()
		case <-tick.C:
		}
	}
}

// refreshIndex brings the index up to date behind the screen: the first frame
// is what was indexed last time, and the new sessions arrive a moment later.
func (a *tuiApp) refreshIndex() {
	a.post(func() { a.indexing = true })
	err := ensureForCLISearch(a.dir, search.Options{}, false, io.Discard)
	a.post(func() {
		a.indexing = false
		if err != nil {
			a.say("Index not updated: "+err.Error(), false)
			return
		}
		go a.loadBehind()
		a.reload()
	})
}

func (a *tuiApp) loadKept() {
	ss, err := tuiKept(a.dir)
	a.post(func() {
		if err != nil {
			return
		}
		a.kept, a.keptLoaded = ss, true
		for _, s := range ss {
			a.keptIDs[sessionKey(s)] = true
		}
		if a.scope == scopeKept {
			a.reload()
		}
	})
}

func (a *tuiApp) detailWorker() {
	for s := range a.loadQ {
		d := tuiLoadDetail(a.dir, s)
		key := sessionKey(s)
		a.post(func() {
			a.details[key] = d
			delete(a.loading, key)
		})
	}
}

// want queues a session for its full read unless it is in hand or on the way.
func (a *tuiApp) want(s model.Session) {
	k := sessionKey(s)
	if a.details[k] != nil || a.loading[k] {
		return
	}
	select {
	case a.loadQ <- s:
		a.loading[k] = true
	default:
	}
}

func (a *tuiApp) say(msg string, good bool) {
	a.toast, a.toastGood, a.toastUntil = msg, good, time.Now().Add(3*time.Second)
}

// reload rebuilds the list for the current scope and query.
func (a *tuiApp) reload() {
	if len(a.query) == 0 {
		a.loadHome()
		return
	}
	a.startSearch()
}

func (a *tuiApp) scopeProjects() []string {
	if a.scope == scopeHere {
		return a.projects
	}
	return nil
}

func (a *tuiApp) loadHome() {
	all, _, err := index.RecentMatchingCounted(a.dir, 0, search.Options{})
	if err == nil {
		a.allMeta = all
		a.agentsAll = tuiAgentCounts(all)
	}
	var ss []model.Session
	total := 0
	switch a.scope {
	case scopeKept:
		ss, total = a.kept, len(a.kept)
	default:
		ss, total, _ = tuiRecent(a.dir, a.scopeProjects(), 60)
	}
	a.setRows(ss, nil, total)
	a.widened = false
}

// setRows replaces the list. A refresh of the same list keeps the selection
// on the same session; a new query or scope starts from the top.
func (a *tuiApp) setRows(ss []model.Session, hits []search.Hit, total int) {
	var keep string
	listed := num(a.scope) + "\x00" + string(a.query)
	if a.sel < len(a.rows) && listed == a.listed {
		keep = sessionKey(a.rows[a.sel].s)
	}
	a.listed = listed
	var rows []tuiRow
	add := func(s model.Session, snips []string) {
		if len(a.filter) > 0 && !a.filter[s.Harness] {
			return
		}
		rows = append(rows, tuiRow{s: s, snips: snips})
	}
	if hits != nil {
		for _, h := range hits {
			if a.scope == scopeKept && !a.keptIDs[sessionKey(h.Session)] {
				continue
			}
			add(h.Session, tuiSnippets(h))
		}
	} else {
		for _, s := range ss {
			add(s, nil)
		}
		if a.scope != scopeKept {
			var behind []behindRow
			for _, b := range a.behind {
				if len(a.filter) == 0 || a.filter[b.s.Harness] {
					behind = append(behind, b)
				}
			}
			label, _ := a.sectionLabel()
			rows = withBehind(behind, rows, label)
		}
	}
	a.rows, a.total = rows, total
	if len(a.filter) > 0 || a.scope == scopeKept {
		a.total = len(rows)
	}
	a.sel, a.scroll = 0, 0
	for i, r := range rows {
		if sessionKey(r.s) == keep {
			a.sel = i
		}
	}
	for i, r := range rows {
		if i > 24 {
			break
		}
		a.want(r.s)
	}
}

func (a *tuiApp) startSearch() {
	a.seq++
	seq, q, projects, scope := a.seq, string(a.query), a.scopeProjects(), a.scope
	a.searching = true
	time.AfterFunc(30*time.Millisecond, func() {
		start := time.Now()
		o := search.Options{Query: q, Projects: projects, Limit: 80}
		hits, err := tuiSearch(a.dir, o)
		widened := false
		if err == nil && len(hits) == 0 && scope == scopeHere && len(projects) > 0 {
			o.Projects = nil
			hits, err = tuiSearch(a.dir, o)
			widened = len(hits) > 0
		}
		took := float64(time.Since(start).Microseconds()) / 1000
		a.post(func() {
			if seq != a.seq {
				return
			}
			a.searching = false
			if err != nil {
				a.say("Search failed: "+err.Error(), false)
				return
			}
			a.tookMS, a.widened = took, widened
			a.setRows(nil, hits, len(hits))
			// The banner offers the newest answer under ↵, so it is the
			// one selected.
			if n, s := dejaVu(q, a.rows); n > 0 {
				for i, r := range a.rows {
					if sessionKey(r.s) == sessionKey(s) {
						a.sel = i
					}
				}
			}
		})
	})
}

func (a *tuiApp) selected() (model.Session, bool) {
	if a.sel < 0 || a.sel >= len(a.rows) {
		return model.Session{}, false
	}
	return a.rows[a.sel].s, true
}

// resumeSelected leaves the screen and reopens the session in its own agent.
func (a *tuiApp) resumeSelected() {
	a.remember()
	s, ok := a.current()
	if !ok {
		return
	}
	if d := a.details[sessionKey(s)]; d != nil && d.gone {
		a.say(agentName(s.Harness)+" deleted this one. R puts it back first.", false)
		return
	}
	a.after = func() error {
		fmt.Fprintf(os.Stderr, "deja: resuming in %s\n", agentName(s.Harness))
		return runResume(a.dir, []string{s.ID, "--exec"}, os.Stdout)
	}
	a.quit = true
}

// putBack writes a session the agent deleted back where it reads it.
func (a *tuiApp) putBack() {
	s, ok := a.current()
	if !ok {
		return
	}
	d := a.details[sessionKey(s)]
	if d == nil || !d.gone {
		a.say("Still in "+agentName(s.Harness)+", nothing to put back.", false)
		return
	}
	go func() {
		err := writeBackSession(a.dir, d.full, io.Discard)
		a.post(func() {
			if err != nil {
				a.say("Could not put it back: "+err.Error(), false)
				return
			}
			d.gone = false
			a.say("Put back. r resumes it in "+agentName(s.Harness)+".", true)
		})
	}()
}

// current is the session the actions apply to: the open one in the reader,
// the selected card otherwise.
func (a *tuiApp) current() (model.Session, bool) {
	if a.view == viewReader {
		return a.reader.s, true
	}
	return a.selected()
}
