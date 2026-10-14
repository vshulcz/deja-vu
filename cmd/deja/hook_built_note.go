package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// builtNote is what the first session start after a build says about what
// the build found: the counts, and on a second line one finding when the
// history holds one. Someone who installed deja from a marketplace
// never runs `deja install` and never sees the proof the CLI prints; for
// them the index was built in silence and recall stayed invisible until it
// happened to fire (#3073). Once per index.
func builtNote(dir string) string { return builtNoteFor(dir, true) }

// builtNoteFor is builtNote for a reader that may not be the owner alone.
// private says the note reaches only the person at this machine: a host's own
// UI line. `hook-context --notes` feeds plugin replies, and OpenClaw's /deja
// answers in whatever chat it was asked in, a group included, so there the
// note names an error and never quotes one of the person's own questions.
func builtNoteFor(dir string, private bool) string {
	if !index.HasManifest(dir) {
		return ""
	}
	marker := dir + ".builtnote"
	if _, err := os.Stat(marker); err == nil {
		return ""
	}
	pol := policy.Load()
	allow := func(project string) bool { return pol.Allows(policy.ActivationSearch, project) }
	sessions, harnesses, repeated := index.BuiltSummary(dir, allow)
	if sessions == 0 {
		// Nothing to report yet; the note waits for the build that finds
		// history rather than spending its one appearance on an empty store.
		return ""
	}
	_ = os.WriteFile(marker, []byte("1"), 0o600)
	// The count and what it found on one line. The sentence that used to
	// explain what an index is for, and the docs on a line of their own, made
	// the first session start four lines tall in every host.
	// A marketplace install never runs `deja install`, so this notice is the
	// first thing deja says to that person. It is also the only place left to
	// say where the docs are (#3030); the site links the tracker.
	//
	// Then one thing the counts cannot say: what the history holds (#4616).
	// Both lookups run over the manifest, like `deja brief`'s, and only on
	// this one session start.
	return joinNotes(builtLine(sessions, harnesses, repeated, whereToLook(dir) != ""), builtFinding(dir, allow, private))
}

// builtFinding is the error this machine keeps hitting, the same wall `deja
// brief` names, or for a private reader the question asked again days apart
// when there is no such error; "" when the history has neither.
func builtFinding(dir string, allow func(string) bool, private bool) string {
	if f, ok := index.FindFriction(dir, allow); ok {
		return builtErrorLine(f.Text, len(f.Sessions), sessionHarnesses(f.Sessions))
	}
	if !private {
		return ""
	}
	if a, ok := index.FindAskedTwice(dir, allow); ok {
		return builtAskedLine(a.Text, len(a.Sessions), sessionHarnesses(a.Sessions))
	}
	return ""
}

// builtErrorLine says it the way the install proof does, cut to one note line.
// The error is cut from the middle: the tool is at the front and what failed
// is at the end, while the middle is a path or a host.
func builtErrorLine(text string, n int, harnesses []string) string {
	agents := builtSessionsIn(n, harnesses)
	const lead = "this machine has hit `"
	room := noteWidth - termwidth.Columns(lead+"`"+agents)
	return lead + elideMiddleColumns(search.SafeLine(text), room) + "`" + agents
}

// builtAskedLine is the question's version, quoted the way the receipt quotes
// a title.
func builtAskedLine(text string, n int, harnesses []string) string {
	agents := builtSessionsIn(n, harnesses)
	const lead = "you asked "
	room := noteWidth - termwidth.Columns(lead+agents)
	return lead + quoteWithin(trimBriefTitleTo(text, 1<<20), room) + agents
}

// builtSessionsIn is " in N sessions (claude, codex)", without the
// parentheses when no session names its agent.
func builtSessionsIn(n int, harnesses []string) string {
	out := " in " + formatStatNumber(n) + " sessions"
	if list := fitList(harnesses, 24); list != "" {
		out += " (" + list + ")"
	}
	return out
}

// elideMiddleColumns is elideMiddleBytes measured in terminal columns.
func elideMiddleColumns(text string, width int) string {
	if termwidth.Columns(text) <= width {
		return text
	}
	if width < 2 {
		return "…"
	}
	room := width - 1
	head := termwidth.Cut(text, room*3/5)
	return head + "…" + termwidth.CutRight(text, room-termwidth.Columns(head))
}

// sessionHarnesses lists the agents behind some sessions, sorted.
func sessionHarnesses(ms []index.SessionMeta) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range ms {
		if m.Harness != "" && !seen[m.Harness] {
			seen[m.Harness] = true
			out = append(out, m.Harness)
		}
	}
	sort.Strings(out)
	return out
}

// builtLine is the built note's text for what the build found.
func builtLine(sessions, harnesses, repeated int, docs bool) string {
	found := fmt.Sprintf("%d agent%s", harnesses, pluralS(harnesses))
	if repeated > 0 {
		found += fmt.Sprintf(", %s repeat question%s", formatStatNumber(repeated), pluralS(repeated))
	}
	line := fmt.Sprintf("deja indexed %s session%s (%s)", formatStatNumber(sessions), pluralS(sessions), found)
	if docs {
		line += " · " + docsLink
	}
	return line
}
