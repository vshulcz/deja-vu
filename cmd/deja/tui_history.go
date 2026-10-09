package main

import (
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/model"
)

// The searches the reader acted on, oldest first, kept beside the index the
// way the release stamp is. ↑ in an empty box walks back through them.

const tuiHistoryMax = 100

func tuiHistoryPath(dir string) string { return dir + ".tui-history" }

func loadTUIHistory(dir string) []string {
	b, err := os.ReadFile(tuiHistoryPath(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// remember records the current query once the reader did something with
// what it found. A query typed and abandoned is not history.
func (a *tuiApp) remember() {
	q := strings.Join(strings.Fields(string(a.query)), " ")
	if q == "" || len(a.rows) == 0 {
		return
	}
	hist := a.history[:0:0]
	for _, h := range a.history {
		if h != q {
			hist = append(hist, h)
		}
	}
	hist = append(hist, q)
	if len(hist) > tuiHistoryMax {
		hist = hist[len(hist)-tuiHistoryMax:]
	}
	a.history = hist
	_ = atomicfile.Write(tuiHistoryPath(a.dir), []byte(strings.Join(hist, "\n")+"\n"), 0o600)
}

// recall steps through history: -1 is older, +1 newer, and stepping past the
// newest clears the box.
func (a *tuiApp) recall(d int) {
	if len(a.history) == 0 {
		return
	}
	i := a.histAt + d
	if a.histAt < 0 {
		i = len(a.history) - 1
	}
	switch {
	case i < 0:
		i = 0
	case i >= len(a.history):
		a.histAt, a.query = -1, nil
		a.reload()
		return
	}
	a.histAt, a.query = i, []rune(a.history[i])
	a.reload()
}

// dejaVu is the moment the screen is named for: the question just typed was
// asked before, in more than one session. It counts the hits whose opening
// question holds every word of this one, and names the newest.
func dejaVu(query string, rows []tuiRow) (int, model.Session) {
	terms := queryTerms(query)
	if len(terms) < 2 {
		return 0, model.Session{}
	}
	n := 0
	var newest model.Session
	for _, r := range rows {
		title := strings.ToLower(r.s.Title)
		all := true
		for _, t := range terms {
			all = all && strings.Contains(title, strings.ToLower(t))
		}
		if !all {
			continue
		}
		n++
		if newest.ID == "" || r.s.Updated.After(newest.Updated) {
			newest = r.s
		}
	}
	if n < 2 {
		return 0, model.Session{}
	}
	return n, newest
}
