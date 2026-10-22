package main

import (
	"io"
	"maps"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
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
	modalNews
	modalForget
	modalDelete
)

type tuiApp struct {
	dir    string
	t      *tui.Term
	p      painter
	now    time.Time
	clock  func() time.Time      // nil: the wall clock; tests pin it
	copy   func(string) error    // nil: the clipboard; tests catch it
	forget func(id string) error // nil: `deja forget --session`; tests swap it

	scope       int
	projects    []string
	cwd         string
	behind      []behindRow
	allMeta     []model.Session
	allStamp    string // the index write allMeta was read from
	agentsAll   []agentCount
	agentsHere  []agentCount // agentsAll in this project, for the strip over its list
	filter      map[string]bool
	query       []rune
	listFocus   bool
	rows        []tuiRow
	listed      string // the scope and query rows answers
	total       int
	hits        []search.Hit // what the search read, in rank order
	asked       int          // how many hits it asked for
	matched     int          // how many sessions matched in all
	loadingMore bool
	exhausted   bool // the last page added nothing
	sel, scroll int
	tookMS      float64
	widened     bool
	byDate      bool // the search list reads newest first, not best first
	searchRows  bool // rows holds a search's hits, not the home list
	rowsByDate  bool // and they were read newest first
	searching   bool
	seq         int
	latest      atomic.Int64 // seq of the newest search, read off the loop

	kept       []model.Session
	keptLoaded bool
	keptIDs    map[string]bool

	details map[string]*tuiDetail
	loading map[string]bool
	loadQ   chan model.Session
	updates chan func()
	bg      sync.WaitGroup // one-shot work off the event loop, see spawn

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
	news      []string // what a new version brings, shown once
	newsVer   string
	leaving   string // the agent the screen is handing over to

	history    []string // past searches, oldest first
	continued  []string // agents continued into, newest first
	histAt     int      // where ↑ is in history, -1 when not browsing
	firstShown int      // the first card on screen, for 1-9
	beyond     beyond   // where an empty answer can go next
	boxMemo    struct {
		key string
		at  time.Time
		f   boxFilters
		ok  bool
	}

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
	} else {
		a.loadNews()
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
	setTheme(terminalTheme())
	a.cwd = howCwd()
	a.projects = howScope(a.cwd, "", false)
	a.history, a.histAt = loadTUIHistory(dir), -1
	a.continued = loadTUIContinued(dir)
	return a
}

func (a *tuiApp) post(f func()) {
	select {
	case a.updates <- f:
	default:
		go func() { a.updates <- f }()
	}
}

// spawn runs f off the event loop and counts it, so a test can wait for the
// screen's background reads and writes to end before its temp dirs go.
func (a *tuiApp) spawn(f func()) {
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		f()
	}()
}

func (a *tuiApp) run() {
	a.loadHome()
	if a.scope == scopeHere && a.total == 0 {
		a.scope = scopeAll
		a.loadHome()
	}
	go a.detailWorker()
	if a.view == viewWelcome {
		a.spawn(a.firstBuild)
	} else {
		a.spawn(a.refreshIndex)
		a.spawn(a.loadKept)
		a.spawn(a.loadBehind)
	}
	// Fast enough for the tail and the spinners to move smoothly; rows that
	// did not change are not written, so an idle screen costs nothing.
	tick := time.NewTicker(100 * time.Millisecond)
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
	// A last frame says where the reader is going before the screen closes.
	if a.leaving != "" {
		a.render()
		time.Sleep(450 * time.Millisecond)
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
		a.spawn(a.loadBehind)
		a.reload()
		if line := manualIndexLine(a.dir); line != "" {
			a.say(strings.TrimPrefix(line, "deja: "), false)
		}
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
	if a.loading[k] {
		return
	}
	// One read on the first frame came from the index as it was; a session
	// that has grown since the refresh is read again.
	if d := a.details[k]; d != nil && (d.err != nil || d.full.ID == "" || !d.full.Updated.Before(s.Updated)) {
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
	if a.box().text == "" {
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
	// A search still running answers a box that is empty now.
	a.seq++
	a.latest.Store(int64(a.seq))
	a.searching = false
	// Every session, for the counts in the header: read again only once the
	// index has been written, not on each tab switch or cleared box, where on
	// a large history it was most of the wait.
	if st := index.Stamp(a.dir); st == "" || st != a.allStamp {
		all, _, err := index.RecentMatchingCounted(a.dir, 0, search.Options{})
		if err == nil {
			a.allMeta, a.allStamp = all, st
			a.agentsAll = tuiAgentCounts(all)
		}
	}
	if a.scope == scopeHere {
		var here []model.Session
		for _, s := range a.allMeta {
			if query.ProjectAnyMatches(s.Project, s.From, a.projects) {
				here = append(here, s)
			}
		}
		a.agentsHere = tuiAgentCounts(here)
	}
	var ss []model.Session
	total := 0
	switch a.scope {
	case scopeKept:
		ss, total = a.kept, len(a.kept)
	default:
		// Filtered, the list is read whole: yesterday's sessions sit behind
		// today's, and a cut taken first would drop them.
		box, n := a.box(), 60
		if box.active() {
			n = 0
		}
		ss, total, _ = tuiRecent(a.dir, box.options(a.scopeProjects()), n)
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
	a.searchRows = hits != nil
	var rows []tuiRow
	box := a.box()
	add := func(s model.Session, snips []string) {
		if len(a.filter) > 0 && !a.filter[s.Harness] || !box.keeps(s) {
			return
		}
		rows = append(rows, tuiRow{s: s, snips: snips})
	}
	if hits != nil {
		strict := false
		for _, h := range hits {
			strict = strict || h.Strict
		}
		for _, h := range hits {
			if a.scope == scopeKept && !a.keptIDs[sessionKey(h.Session)] {
				continue
			}
			n := len(rows)
			add(h.Session, tuiSnippets(h))
			if len(rows) > n {
				rows[n].partial = strict && !h.Strict
			}
		}
		if a.rowsByDate {
			a.daySections(rows)
		}
	} else {
		for _, s := range ss {
			add(s, nil)
		}
		if a.scope != scopeKept && !box.active() {
			var behind []behindRow
			for _, b := range a.behind {
				if len(a.filter) == 0 || a.filter[b.s.Harness] {
					behind = append(behind, b)
				}
			}
			a.daySections(rows)
			rows = withBehind(behind, rows)
		}
	}
	a.rows, a.total = rows, total
	if len(a.filter) > 0 || a.scope == scopeKept || box.active() {
		a.total = len(rows)
	}
	scroll := a.scroll
	a.sel, a.scroll = 0, 0
	for i, r := range rows {
		if sessionKey(r.s) == keep {
			a.sel, a.scroll = i, scroll
		}
	}
	for i, r := range rows {
		if i > 24 {
			break
		}
		a.want(r.s)
	}
}

// daySections heads each run of cards from the same day group, for a list
// that reads newest first.
func (a *tuiApp) daySections(rows []tuiRow) {
	now := time.Now()
	if a.clock != nil {
		now = a.clock()
	}
	last := ""
	for i := range rows {
		g := dayGroup(rows[i].s.Updated, now)
		// A relevance answer lists the sessions holding every word first,
		// then the rest, each newest first: the rest are headed apart.
		if rows[i].partial {
			g += " · some of the words"
		}
		if g != last {
			rows[i].section, last = g, g
		}
	}
}

// toggleSort switches the search list between best first and newest first.
// The newest are read from the whole match, a page at a time like the
// ranking, not sorted out of the page already listed (#5577).
func (a *tuiApp) toggleSort() {
	a.byDate = !a.byDate
	// A search on screen says its order in its label; the home list is
	// newest first either way, so there the order is said for the next search.
	if a.box().text != "" {
		a.startSearch()
		return
	}
	if a.byDate {
		a.say("Searches list newest first", true)
	} else {
		a.say("Searches list best match first", true)
	}
}

// startSearch runs the box's search from its first page. A refresh of the
// same list reads as far as the reader had scrolled.
func (a *tuiApp) startSearch() {
	limit := tuiPage
	if a.listed == num(a.scope)+"\x00"+string(a.query) && a.asked > limit {
		limit = a.asked
	}
	a.searchPage(limit, false)
}

// moreLeft says whether the match holds sessions past what the list read.
func (a *tuiApp) moreLeft() bool {
	return len(a.query) > 0 && !a.searching && !a.exhausted && a.matched > a.asked
}

// loadMore asks for the next page once the selection nears the end of the
// list. The list reads a page at a time: ranking every session a common word
// matched took 16 s on a 40k-session store before the first card (#5487).
func (a *tuiApp) loadMore() {
	if a.loadingMore || !a.moreLeft() || a.sel < len(a.rows)-tuiMoreAhead {
		return
	}
	a.loadingMore = true
	a.searchPage(a.asked+tuiPage, true)
}

// searchPage runs the box's search for its first limit hits. With next it
// extends the list on screen: the cards already there keep their places and
// the new ones follow.
func (a *tuiApp) searchPage(limit int, next bool) {
	a.seq++
	seq, q, scope, box := a.seq, string(a.query), a.scope, a.box()
	o := box.options(a.scopeProjects())
	o.Limit = limit
	byDate := a.byDate
	if byDate {
		o.Sort = search.SortDate
	}
	if next && a.widened {
		o.Projects = nil
	}
	if !next {
		a.searching = true
	}
	a.latest.Store(int64(seq))
	// A search already overtaken by the next keystroke is not run: on a large
	// history each one is a full pass, and a word typed at speed started one
	// per letter, which held the screen still for seconds.
	stale := func() bool { return a.latest.Load() != int64(seq) }
	a.bg.Add(1)
	time.AfterFunc(60*time.Millisecond, func() {
		defer a.bg.Done()
		if stale() {
			return
		}
		start := time.Now()
		hits, matched, err := tuiSearchPage(a.dir, o)
		widened := next && a.widened
		// A project named with in: is what was asked for; only the tab's
		// scope widens on its own.
		if !next && err == nil && len(hits) == 0 && scope == scopeHere && len(o.Projects) > 0 && len(box.projects) == 0 && !stale() {
			wide := o
			wide.Projects = nil
			hits, matched, err = tuiSearchPage(a.dir, wide)
			widened = len(hits) > 0
		}
		took := float64(time.Since(start).Microseconds()) / 1000
		a.post(func() {
			if seq != a.seq {
				return
			}
			a.searching, a.loadingMore = false, false
			if err != nil {
				a.say("Search failed: "+err.Error(), false)
				return
			}
			if next {
				// A page that added no hit ends the list where it is: the
				// count above it read the postings, and they promised more
				// than the text holds.
				hits = appendNewHits(a.hits, hits)
				a.exhausted = len(hits) == len(a.hits)
			} else {
				a.tookMS, a.exhausted = took, false
			}
			a.hits, a.asked, a.matched = hits, limit, matched
			a.widened = widened
			a.rowsByDate = byDate
			refresh := a.listed == num(a.scope)+"\x00"+q
			a.setRows(nil, hits, len(hits))
			if len(a.rows) == 0 && scope != scopeKept {
				listed, filter := a.listed, maps.Clone(a.filter)
				a.spawn(func() { a.lookBeyond(listed, o, box, filter) })
			}
			// The banner offers the newest answer under ↵, so it is the
			// one selected, on a new query only: a refresh of the same one
			// keeps the reader's pick, which r or o is about to act on.
			if n, s := dejaVu(box.text, a.rows); n > 0 && !refresh {
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

// current is the session the actions apply to: the open one in the reader,
// the selected card otherwise.
func (a *tuiApp) current() (model.Session, bool) {
	if a.view == viewReader {
		return a.reader.s, true
	}
	return a.selected()
}
