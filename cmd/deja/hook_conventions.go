package main

import (
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
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
	// And the ones the team agreed on elsewhere. A promotion that arrived by
	// sync is not in this machine's notes file — it is a session carrying its
	// state, under the source machine's project name (#975), so neither half of
	// the loop above can see it and the block quietly stopped naming the
	// decisions syncing exists to share (#2512). Scoped by the rule every
	// automatic surface shares (#2333) and by the auto activation, since this
	// block is read unasked.
	picked = append(picked, importedConventions(names)...)
	if len(picked) == 0 {
		return ""
	}
	// Newest first: a later decision outranks an older one on the same topic.
	sort.Slice(picked, func(i, j int) bool { return picked[i].At.After(picked[j].At) })

	var b strings.Builder
	b.WriteString("standing decisions in this project (promoted and still accepted — follow them unless the user overrides):\n")
	head := b.Len()
	shown := 0
	// One decision, one line. A decision promoted here and synced to a peer
	// comes back as their copy of it, so both are in hand — and two peers who
	// both took it send two. The block is six lines and a reminder; saying the
	// same thing twice spends one of them and reads as two agreements.
	seen := map[string]bool{}
	for _, note := range picked {
		if shown >= maxNotes {
			break
		}
		line := conventionLine(note)
		if line == "" {
			continue
		}
		key := conventionKey(line)
		if seen[key] {
			continue
		}
		seen[key] = true
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

// importedConventions reads the standing decisions that arrived by sync. The
// manifest holds them, so this costs a map walk rather than a read; the same
// shape the view page (#2421) and the line before an edit (#2511) read.
func importedConventions(names []string) []sources.PromotedNote {
	pol := policy.Load()
	metas := index.PromotedNoteMetas(index.DefaultDir(), func(project string) bool {
		return pol.Allows(policy.ActivationAuto, project)
	})
	var out []sources.PromotedNote
	for _, meta := range metas {
		if meta.Lifecycle != "accepted" {
			continue
		}
		if !inAnyProject(meta.Project, names) {
			continue
		}
		when := meta.Updated
		if t, err := time.Parse(time.RFC3339, meta.LifecycleAt); err == nil {
			when = t
		} else if t, err := time.Parse("2006-01-02", meta.LifecycleAt); err == nil {
			when = t
		}
		text := meta.LifecycleNote
		if strings.TrimSpace(text) == "" {
			text = meta.Title
		}
		out = append(out, sources.PromotedNote{
			Project: meta.Project, State: meta.Lifecycle, Title: meta.Title, Text: text, At: when,
		})
	}
	return out
}

// conventionKey is what makes two renderings of one decision the same. A copy
// that arrived from a peer carries the provenance the promoting machine wrote
// into it — "… (from claude:dec, 2026-08-29)" — so the tail is dropped before
// comparing, and the rest is compared without case.
func conventionKey(line string) string {
	s := strings.TrimSpace(line)
	if i := strings.LastIndex(s, "(from "); i > 0 && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[:i])
	}
	return strings.ToLower(s)
}

// inAnyProject asks the shared question: is this session's project the one the
// reader is standing in. It strips `imported:` and matches a peer's project by
// suffix, which is what makes a decision from another machine belong to the
// project it was made in rather than to a name that happens to match.
func inAnyProject(project string, names []string) bool {
	for _, n := range names {
		if n != "" && index.ProjectInScope(project, n) {
			return true
		}
	}
	return false
}

// conventionLine is the one line to show for a promoted note: its title, or the
// first sentence of its body when it has no title. Kept short — the block is a
// reminder of what was decided, not the reasoning, which recall_context carries.
func conventionLine(n sources.PromotedNote) string {
	// The decision first. A note's title is the session's own opening line —
	// usually the problem someone brought — and the decision is what they
	// wrote with `--note`, so leading with the title told an agent to follow
	// "the migration keeps failing on retry" (#2456). The title is the
	// fallback for a note that has nothing else, which is what `deja promote`
	// writes without `--note`.
	text := strings.TrimSpace(strings.ReplaceAll(n.Text, "\n", " "))
	if text == "" {
		return strings.TrimSpace(n.Title)
	}
	for i, r := range text {
		if (r == '.' || r == '!' || r == '?') && (i+1 >= len(text) || text[i+1] == ' ') {
			return strings.TrimSpace(text[:i+1])
		}
	}
	// Rune-safe: a 200-byte cut lands mid-character on anything but ASCII, and
	// the line goes to an agent as invalid UTF-8 — reproduced on a Russian note
	// and on a Chinese one (#1319).
	return truncatePlanBytes(text, 200)
}
