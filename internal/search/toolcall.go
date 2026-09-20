package search

import (
	"regexp"
	"strings"
)

// dejaCallNames are the tools an agent calls to reach deja. A transcript line
// carrying one of them beside a JSON argument object is the record of a call,
// not something anyone said.
var dejaCallNames = []string{
	"deja_recall_context", "deja_recall", "deja_remember",
	"deja_blame", "deja_fix", "deja_how",
	"recall_context",
}

// withoutOwnCallLog removes the lines where a transcript recorded a call to
// deja, so a question does not match the log of that same question being asked.
//
// An agent run from inside a session writes its stdout into that session's
// transcript, tool-call lines and all, and those lines carry the queries it
// sent. Asked which wording option had been chosen, recall led with
// `⚙ deja_recall {"query":"deja-vu repository description…"}` from a real
// working session, and the agent answered with an invented phrase (#2067).
//
// The lines are removed from matching only. The fact that a call happened stays
// in the transcript, which is what `deja how` and `deja fix` are built on —
// dropping it at ingest would buy this at their cost.
//
// Deliberately narrow: deja's own tool names beside a JSON object. A general
// rule about tool logs would have to guess at every harness's formatting, and
// the echo that matters is the one deja creates for itself.
func withoutOwnCallLog(text string) string {
	if !strings.Contains(text, `{"`) {
		return text
	}
	lines := strings.Split(text, "\n")
	kept := lines[:0:0]
	for _, line := range lines {
		if isOwnCallLine(line) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == len(lines) {
		return text
	}
	return strings.Join(kept, "\n")
}

// isOwnCallLine reports whether a line records a call to deja: one of its tool
// names, and a JSON argument object after it.
func isOwnCallLine(line string) bool {
	low := strings.ToLower(line)
	for _, name := range dejaCallNames {
		at := strings.Index(low, name)
		if at < 0 {
			continue
		}
		// The arguments follow the name — a line merely discussing the tool
		// ("recall_context returns a digest") has no object after it and is
		// ordinary prose worth keeping.
		if strings.Contains(low[at+len(name):], `{"`) {
			return true
		}
	}
	return false
}

// dejaReportLine reports whether a transcript line is deja's own output rather
// than someone talking about a file. An agent exercising deja from a shell
// writes headers and results into its own transcript — `=== deja blame
// internal/index/retrieval.go ===` and the report under it — and every one of
// those lines names the file, so blame ranked them as that file's history and
// quoted them back. Measured on this store: the top two snippets for
// `internal/index/retrieval.go` were deja's own help output.
//
// The same rule the report guard and the fix miner already apply (#2067,
// #2068, #2169): deja's own words must not become what it knows.
//
// Only the command echo, not the report body: a line opening `deja: ` is how
// deja addresses a terminal, but it is also how a person writes about deja,
// and dropping it changed nothing measurable here.
func dejaReportLine(line string) bool {
	l := strings.TrimSpace(line)
	if strings.HasPrefix(l, "=== deja ") || strings.HasPrefix(l, "$ deja ") {
		return true
	}
	return dejaLineAnswerLine(l)
}

// dejaLineAnswerLine recognises `deja blame <path>:<line>`'s own answer.
//
// Same reason the two above are recognised: a transcript that ran deja keeps
// its output, every line of that output names the file, and blame then ranks
// its own past answer as the history of the file and quotes it back (#1330).
// The line answer added a new shape and it is this one (#1181).
func dejaLineAnswerLine(l string) bool {
	if strings.HasPrefix(l, "written in ") && strings.Contains(l, " · ") {
		return true
	}
	if strings.HasPrefix(l, "why, in full: deja ctx ") {
		return true
	}
	// The silence sentence, by its opening rather than in full: it was
	// rewritten when the written side landed and this pinned the old wording
	// word for word (#3773). Nothing measurable was lost while it did — the
	// sentence names no file, so it is never picked as evidence about one on
	// its own — but the opening is what the rule is about, and the header above
	// it does name the file.
	if strings.HasPrefix(l, "no indexed session wrote ") {
		return true
	}
	// The line answer as JSON is one line and names itself on it (#3723).
	if strings.Contains(l, `"kind":"deja.blame-line"`) {
		return true
	}
	// The turn the line answer quotes, which is transcript text: the label in
	// front of it is the only part that is deja's, so it is what this keys on.
	if strings.HasPrefix(l, "said just before this edit: ") {
		return true
	}
	return dejaLineHeader.MatchString(l) || dejaGitNoteLine.MatchString(l)
}

// dejaLineHeader is the answer's first line: `pool.go:3 last changed in abc1234`.
var dejaLineHeader = regexp.MustCompile(`^[^\s:]+:\d+ last changed in [0-9a-f]{7,40}\b`)

// dejaGitNoteLine is the note `blame --git-note` writes on a commit, which
// reaches a transcript the moment someone runs `git log --notes=deja` in a
// session — the same loop as the answer itself, one remove further out.
var dejaGitNoteLine = regexp.MustCompile(`^deja: [^\s:]+:\d+ written in `)

// mayHoldOwnReport is the cheap check that decides whether a message is worth
// splitting into lines at all — blame runs this over every message of every
// candidate session.
//
// It used to be "does the text contain deja", which is true of most of deja's
// output and false of one shape: the line answer for a line nothing is
// attributed to is a header and a sentence, and neither says "deja". So that
// answer was never filtered, and it is the answer that names the file twice
// (#3723).
func mayHoldOwnReport(text string) bool {
	return strings.Contains(text, "deja") ||
		strings.Contains(text, "last changed in") ||
		strings.Contains(text, "no indexed session") ||
		// The quoted turn under a line answer says nothing about deja either:
		// the label is deja's and the sentence after it is someone's own words
		// (#3723).
		strings.Contains(text, "said just before this edit")
}

// WithoutOwnReport is withoutOwnReport for a caller outside this package:
// `blame --attribution` quotes the turn an edit sits in, and that turn can be
// a session running deja and reading its answer back (#3723).
func WithoutOwnReport(text string) string { return withoutOwnReport(text) }

// withoutOwnReport drops those lines, leaving everything a person wrote.
func withoutOwnReport(text string) string {
	if !mayHoldOwnReport(text) {
		return text
	}
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if dejaReportLine(l) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}
