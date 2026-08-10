package main

import (
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// projectConventions returns a compact, query-independent block of the
// decisions a user promoted and still accepts in this project.
//
// Recall surfaces a promoted note only when the query happens to match its
// text, so a standing decision — "we use pgx, not database/sql" — stays silent
// on every task that does not name it, which is most of them. The agent then
// re-decides something the user already settled. This block states those
// decisions up front, before the agent makes a choice, so it follows them
// without anyone having to mention them.
//
// Only accepted notes for the current project, newest first, capped by count
// and bytes. rejected/superseded/stale states are left out: LoadPromotedNotes
// keeps the latest state per source, so a decision that was later reversed
// carries a non-accepted state and never reaches here.
func projectConventions(names []string, maxNotes, budget int) string {
	if len(names) == 0 || maxNotes <= 0 || budget <= 0 {
		return ""
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var picked []sources.PromotedNote
	for _, n := range sources.LoadPromotedNotes() {
		if n.State != "accepted" || !want[n.Project] {
			continue
		}
		picked = append(picked, n)
	}
	if len(picked) == 0 {
		return ""
	}
	// Newest first: a later decision outranks an older one on the same topic.
	sort.Slice(picked, func(i, j int) bool { return picked[i].At.After(picked[j].At) })

	var b strings.Builder
	b.WriteString("standing decisions in this project (promoted and still accepted — follow them unless the user overrides):\n")
	head := b.Len()
	shown := 0
	for _, note := range picked {
		if shown >= maxNotes {
			break
		}
		line := conventionLine(note)
		if line == "" {
			continue
		}
		row := "  · " + search.SafeLine(line) + "\n"
		if b.Len()+len(row) > budget {
			break
		}
		b.WriteString(row)
		shown++
	}
	if b.Len() == head {
		return ""
	}
	return b.String()
}

// conventionLine is the one line to show for a promoted note: its title, or the
// first sentence of its body when it has no title. Kept short — the block is a
// reminder of what was decided, not the reasoning, which recall_context carries.
func conventionLine(n sources.PromotedNote) string {
	if t := strings.TrimSpace(n.Title); t != "" {
		return t
	}
	text := strings.TrimSpace(strings.ReplaceAll(n.Text, "\n", " "))
	if text == "" {
		return ""
	}
	for i, r := range text {
		if (r == '.' || r == '!' || r == '?') && (i+1 >= len(text) || text[i+1] == ' ') {
			return strings.TrimSpace(text[:i+1])
		}
	}
	const cap = 200
	if len(text) > cap {
		return strings.TrimSpace(text[:cap])
	}
	return text
}
