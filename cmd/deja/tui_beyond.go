package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// An empty answer says where to go next: to the other projects when the
// query is there and not here, to the spelling the history uses when one
// word is off, and to fewer words when neither is true.

type beyond struct {
	listed  string // the list this answers; stale once the box moves on
	others  int    // sessions the same query finds in other projects
	suggest string // the query respelled, when that finds something
	found   int
}

// lookBeyond runs off the loop after a search came back empty: the same
// search across every project, then the query with its unknown words
// respelled. Counts pass the same agent and time filters the list does.
func (a *tuiApp) lookBeyond(listed string, o search.Options, box boxFilters, agents map[string]bool) {
	count := func(o search.Options) int {
		hits, err := tuiSearch(a.dir, o)
		if err != nil {
			return 0
		}
		n := 0
		for _, h := range hits {
			if (len(agents) == 0 || agents[h.Session.Harness]) && box.keeps(h.Session) {
				n++
			}
		}
		return n
	}
	b := beyond{listed: listed}
	if len(o.Projects) > 0 {
		wide := o
		wide.Projects = nil
		b.others = count(wide)
	}
	if b.others == 0 {
		if s := index.DidYouMean(a.dir, o.Query); s != "" {
			try := o
			try.Query = s
			if n := count(try); n > 0 {
				b.suggest, b.found = s, n
			}
		}
	}
	a.post(func() {
		if a.listed == listed {
			a.beyond = b
		}
	})
}

// wayOut is the offer for the empty list on screen, or none.
func (a *tuiApp) wayOut() beyond {
	if a.beyond.listed != a.listed || len(a.rows) > 0 || a.searching {
		return beyond{}
	}
	return a.beyond
}

// takeWayOut follows the empty answer's offer, and says whether there was one.
func (a *tuiApp) takeWayOut() bool {
	b := a.wayOut()
	box := a.box()
	switch {
	case b.others > 0 && len(box.projects) > 0:
		// The project was named in the box, so it is the name that goes.
		a.query = []rune(strings.Join(append(box.chipWords(a.query, "in:"), box.text), " "))
		a.reload()
	case b.others > 0:
		a.setScope(scopeAll)
	case b.suggest != "":
		a.query = []rune(strings.Join(append(box.chipWords(a.query, ""), b.suggest), " "))
		a.reload()
	default:
		return false
	}
	return true
}

// chipWords is the box's filters as typed, without those that start with
// drop (none dropped when drop is empty).
func (f boxFilters) chipWords(q []rune, drop string) []string {
	var out []string
	for _, c := range f.chips {
		w := string(q[c[0]:c[1]])
		if drop == "" || !strings.HasPrefix(strings.ToLower(w), drop) {
			out = append(out, w)
		}
	}
	return out
}
