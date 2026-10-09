package main

import (
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The search box takes a few words as filters rather than as text: an agent
// with a colon (codex:), a time word (today, yesterday, week, month) and
// in:<project>. The box draws them as chips, the search applies them, and
// everything else is the query. A word the screen does not know, foo: among
// them, stays text.

type boxFilters struct {
	text      string   // what is left to search for
	harnesses []string // agent ids, any of them
	since     time.Time
	until     time.Time // zero: up to now
	when      string    // the time word as typed, for the label
	projects  []string  // in:<project>, replacing the tab's scope
	chips     [][2]int  // rune ranges of the box that are filters
}

func (f boxFilters) active() bool {
	return len(f.harnesses) > 0 || !f.since.IsZero() || len(f.projects) > 0
}

// keeps says whether a session passes the filters the index cannot apply
// itself: more than one agent, and the end of yesterday.
func (f boxFilters) keeps(s model.Session) bool {
	if len(f.harnesses) > 0 {
		ok := false
		for _, h := range f.harnesses {
			ok = ok || s.Harness == h
		}
		if !ok {
			return false
		}
	}
	if !f.since.IsZero() && s.Updated.Before(f.since) {
		return false
	}
	return f.until.IsZero() || s.Updated.Before(f.until)
}

// options is the search the filters ask for, on top of the tab's projects.
func (f boxFilters) options(projects []string) search.Options {
	o := search.Options{Query: f.text, Projects: projects}
	if len(f.projects) > 0 {
		o.Projects = f.projects
	}
	if len(f.harnesses) == 1 {
		o.Harness = f.harnesses[0]
	}
	if !f.since.IsZero() {
		// The index measures back from the wall clock; a minute of slack
		// keeps the first second of the day in, and keeps cuts it exactly.
		o.Since = time.Since(f.since) + time.Minute
	}
	return o
}

// agentByWord is the harness a typed word names: its id, or its display
// name run together (claudecode:, codexcli:).
func agentByWord(w string) (string, bool) {
	w = strings.ToLower(w)
	if _, ok := agentNames[w]; ok {
		return w, true
	}
	for id := range agentNames {
		if strings.ToLower(strings.ReplaceAll(agentName(id), " ", "")) == w {
			return id, true
		}
	}
	return "", false
}

// parseBox splits what was typed into filters and the query.
func parseBox(q []rune, now time.Time) boxFilters {
	type word struct {
		s          string
		start, end int
	}
	var words []word
	for i := 0; i < len(q); {
		if unicode.IsSpace(q[i]) {
			i++
			continue
		}
		j := i
		for j < len(q) && !unicode.IsSpace(q[j]) {
			j++
		}
		words = append(words, word{string(q[i:j]), i, j})
		i = j
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var f boxFilters
	var text []string
	chip := func(w word) { f.chips = append(f.chips, [2]int{w.start, w.end}) }
	for i := 0; i < len(words); i++ {
		w := words[i]
		low := strings.ToLower(w.s)
		if name, ok := strings.CutSuffix(low, ":"); ok && name != "" {
			if h, ok := agentByWord(name); ok {
				f.harnesses = append(f.harnesses, h)
				chip(w)
				continue
			}
		}
		if p, ok := strings.CutPrefix(low, "in:"); ok && p != "" {
			f.projects = append(f.projects, w.s[3:])
			chip(w)
			continue
		}
		span := w
		if low == "last" && i+1 < len(words) {
			if next := strings.ToLower(words[i+1].s); next == "week" || next == "month" {
				i++
				span.end, low = words[i].end, next
			}
		}
		since, until := time.Time{}, time.Time{}
		switch low {
		case "today":
			since = day
		case "yesterday":
			since, until = day.AddDate(0, 0, -1), day
		case "week":
			since = now.AddDate(0, 0, -7)
		case "month":
			since = now.AddDate(0, -1, 0)
		default:
			text = append(text, w.s)
			continue
		}
		f.since, f.until, f.when = since, until, strings.ToLower(string(q[span.start:span.end]))
		chip(span)
	}
	f.text = strings.Join(text, " ")
	return f
}

// box is the filters in the box as it reads now.
func (a *tuiApp) box() boxFilters { return parseBox(a.query, a.wall()) }

func (a *tuiApp) wall() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// drawQuery writes the box's text with its filters as chips. A query too long
// for the box keeps its end, where the cursor is.
func (a *tuiApp) drawQuery(x, y, max int) int {
	p := a.p
	q := a.query
	start := 0
	for start < len(q) && termwidth.Columns(string(q[start:])) > max-x-1 {
		start++
	}
	if start > 0 {
		x = p.Put(x, y, "…", fgs(cMuted), max)
		start++
	}
	in := make([]bool, len(q))
	for _, c := range a.box().chips {
		for i := c[0]; i < c[1]; i++ {
			in[i] = true
		}
	}
	chip := tui.Style{FG: cAcc, BG: cOver, Bold: true, Reverse: a.p.mono}
	for i := start; i < len(q); {
		j := i + 1
		for j < len(q) && in[j] == in[i] {
			j++
		}
		st := bold(cText)
		if in[i] {
			st = chip
		}
		x = p.Put(x, y, string(q[i:j]), st, max)
		i = j
	}
	return x
}

// filterNote names the filters in force for the list's label.
func (f boxFilters) note() string {
	var parts []string
	for _, h := range f.harnesses {
		parts = append(parts, agentName(h))
	}
	if f.when != "" {
		parts = append(parts, f.when)
	}
	for _, p := range f.projects {
		parts = append(parts, "in "+p)
	}
	return strings.Join(parts, " · ")
}
