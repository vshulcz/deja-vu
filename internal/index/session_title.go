package index

import (
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// sessionTitleFrom derives a session's title and reports whether it is the
// agent's own words rather than the reader's.
//
// A user turn always wins; the assistant's opening line fills in when there is
// none — a session the agent opened itself, or one whose prompts are all
// harness plumbing. The alternative was the blank line these printed in `deja
// last` and on the first screen (#692). A session of nothing but tool output
// has neither, and printed an empty bracket in `last` against a dash in
// `stats`; it is named after its first output instead.
func sessionTitleFrom(s model.Session) (title string, fromAgent bool) {
	if t := earliestTitle(s.Messages, "user"); t != "" {
		if thinTitle(t) {
			// A session that opens with a greeting was named after it: on a real
			// 800-session store, 17 rows read "hi" and 3 "привет", with the turn
			// that says what the work was one line below (#790). No word list —
			// any of those is language-bound, and this store has two languages
			// in the sample already. A turn too short to name anything gives way
			// to the next one that can, and stands on its own when there is
			// none.
			if next := nextSubstantialTitle(s.Messages, t); next != "" {
				return next, false
			}
		}
		return t, false
	}
	if t := earliestTitle(s.Messages, "assistant"); t != "" {
		return t, true
	}
	if t := earliestTitle(s.Messages, roleToolOutput); t != "" {
		return toolOutputTitle(t), false
	}
	// Nothing here is worth a title on its own — every turn is harness
	// plumbing, a CLI's own stdout or a notification the runtime spliced in.
	// #1100 named the tool-only session so it would stop listing as an empty
	// bracket; this is the same session one step further down. The surfaces
	// that used to recover a title read .Messages, and they are all fed by
	// Recent, which returns metadata alone, so the recovery never ran (#2548).
	if t := earliestAnyText(s.Messages); t != "" {
		return harnessOutputTitle(t), false
	}
	return "", false
}

// harnessOutputTitlePrefix marks a title borrowed from what the harness itself
// wrote, the way toolOutputTitlePrefix marks one borrowed from tool output: it
// rides in the text so every surface says the same thing.
const harnessOutputTitlePrefix = "harness output: "

// TitleIsBorrowed reports that a title is not what a person typed: the ingest
// borrows one from what the harness or a tool wrote when a session opens with
// plumbing, and marks it in the text. A surface that picks a phrase out of
// titles has to skip those, or it suggests a fleet's paperwork (#3714).
func TitleIsBorrowed(title string) bool {
	t := strings.TrimSpace(title)
	return strings.HasPrefix(t, harnessOutputTitlePrefix) || strings.HasPrefix(t, toolOutputTitlePrefix)
}

// titlePlaceholder reports that a title is only standing in until the session
// says something of its own. The incremental path fills a title in when it is
// empty; a session that opens with a slash command would otherwise keep the
// stand-in forever, with the reader's own first question one line below it.
func titlePlaceholder(t string) bool {
	return t == "" || strings.HasPrefix(t, harnessOutputTitlePrefix)
}

// titleRankOfRow is where a row's title sits in sessionTitleFrom's order: a
// question (4), a turn too thin to name anything (3), the agent's words (2),
// tool output (1), plumbing or nothing (0). A pass that sees only part of a
// session keeps a title unless what it read outranks it, as a rebuild of the
// whole session would: a thin opening gives way to the first real question.
func titleRankOfRow(title string, fromAgent bool) int {
	switch {
	case titlePlaceholder(title):
		return 0
	case strings.HasPrefix(title, toolOutputTitlePrefix):
		return 1
	case fromAgent:
		return 2
	case thinTitle(title):
		return 3
	}
	return 4
}

func harnessOutputTitle(t string) string {
	return harnessOutputTitlePrefix + truncateTitle(stripPlumbingTag(t), 60-len([]rune(harnessOutputTitlePrefix)))
}

// stripPlumbingTag unwraps the element a harness wraps its own output in —
// <local-command-stdout>…</local-command-stdout> and its neighbours — so the
// row reads as the sentence rather than as markup.
func stripPlumbingTag(t string) string {
	t = strings.TrimSpace(t)
	if !strings.HasPrefix(t, "<") {
		return t
	}
	end := strings.Index(t, ">")
	if end < 0 {
		return t
	}
	name := t[1:end]
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	rest := strings.TrimSpace(t[end+1:])
	rest = strings.TrimSuffix(rest, "</"+name+">")
	return strings.TrimSpace(rest)
}

// earliestAnyText is the first thing a session says, whoever said it, for the
// last resort above.
func earliestAnyText(ms []model.Message) string {
	best := ""
	var bestAt time.Time
	for _, msg := range ms {
		// Work records are not something anyone said — a file list, an
		// invocation, a replaced span, a line hash — and a session named after
		// one reads as a sentence it never contained.
		if msg.Role == roleFiles || msg.Role == roleCommand || msg.Role == roleEdit || msg.Role == roleWrote {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if t == "" {
			continue
		}
		switch {
		case best == "":
		case bestAt.IsZero(), msg.Time.IsZero():
			continue
		case !msg.Time.Before(bestAt):
			continue
		}
		best, bestAt = t, msg.Time
	}
	return best
}

// toolOutputTitlePrefix marks a title borrowed from tool output. It rides in
// the title text rather than in a flag beside it so that every surface —
// `last`, `stats`, the MCP listing, a synced peer — says the same thing
// without a second field having to travel with it.
const toolOutputTitlePrefix = "tool output: "

func toolOutputTitle(t string) string {
	return toolOutputTitlePrefix + truncateTitle(t, 60-len([]rune(toolOutputTitlePrefix)))
}

// earliestTitle picks the first turn of a role by the clock, falling back to
// file order for records that carry no time.
//
// Taking whichever line sat first in the file disagreed with the import path,
// which grew this fallback in #692 and orders by time — so one store titled
// locally and the same store imported elsewhere could disagree (#769).
func earliestTitle(ms []model.Message, role string) string {
	best := ""
	var bestAt time.Time
	for _, msg := range ms {
		if msg.Role != role {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if !titleWorthy(t) {
			continue
		}
		switch {
		case best == "":
		case bestAt.IsZero(), msg.Time.IsZero():
			// Nothing to compare: the first one found stands.
			continue
		case !msg.Time.Before(bestAt):
			continue
		}
		best, bestAt = t, msg.Time
	}
	return best
}

// thinTitle reports that a turn is too short to name a session by.
//
// Two words and twelve runes: "hi", "привет", "say ok", "reply ok" are inside
// it, and the short instructions worth keeping — "fix the build", "run the
// tests" — are not. A length rule rather than a vocabulary is the only version
// of this that works in every language the store holds.
func thinTitle(t string) bool {
	return titleWords(t) <= 2 && len([]rune(t)) <= 12
}

// titleWords counts what a reader would call words. Whitespace separates them
// in most scripts and in none of the CJK ones, where a whole sentence is one
// field and a rune-length rule alone called it a greeting: 为什么测试失败了 is
// eight runes and says why the test failed (review of #3328). Thai is spaced
// the same way, which is why Unspaced covers both.
func titleWords(t string) int {
	words, inWord := 0, false
	for _, r := range t {
		switch {
		// Hangul is in cjkfold's CJK set because that is what folds, but Korean
		// writes its words apart like Latin does: counting each syllable as a
		// word made "고마워" — one word, three syllables — look substantial and
		// left it as a session's name (review of #3328).
		case cjkfold.Unspaced(r) && !unicode.Is(unicode.Hangul, r):
			words++
			inWord = false
		case unicode.IsSpace(r):
			inWord = false
		default:
			if !inWord {
				words++
				inWord = true
			}
		}
	}
	return words
}

// nextSubstantialTitle is the first later user turn that can name the session.
// Ordered by the clock like earliestTitle, so a store titled locally and the
// same store imported elsewhere agree.
func nextSubstantialTitle(ms []model.Message, skip string) string {
	best := ""
	var bestAt time.Time
	for _, msg := range ms {
		if msg.Role != "user" {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if t == skip || !titleWorthy(t) || thinTitle(t) {
			continue
		}
		switch {
		case best == "":
		case bestAt.IsZero(), msg.Time.IsZero():
			continue
		case !msg.Time.Before(bestAt):
			continue
		}
		best, bestAt = t, msg.Time
	}
	return best
}

// titleWorthy reports whether a user turn is the sentence a person would
// recognise the session by. Harness plumbing arrives with the user role — a
// slash command's expansion, a task notification, the compaction caveat — and
// naming a session after one of those is how the titles in #636 happened.
func titleWorthy(t string) bool {
	// The same list the repeat-question counter rejects: the two were written
	// apart and drifted, so a session whose store title was thin could be
	// renamed after a resume preamble — "This session is being continued from a
	// previous conversation…" — which notAsked has rejected all along (review
	// of #3328).
	return strings.TrimSpace(t) != "" && !harnessPreamble(t)
}

// widenThinSourceTitle gives a name too short to name anything way to the
// question under it, the way a derived title has since #790: dsh's title model
// answered "ok" and "47" for sessions whose user turn is a whole sentence — 39
// of the 43 on this machine's store (#3328). Notes name themselves.
func widenThinSourceTitle(s model.Session, title string) string {
	if s.Harness == "deja" || !thinTitle(title) {
		return title
	}
	next := nextSubstantialTitle(s.Messages, title)
	if next == "" {
		return title
	}
	next, _ = redact.Text(next)
	return truncateTitle(next, 60)
}

// boundSourceTitle collapses and bounds a title the source authored, the way
// a derived one has always been.
//
// Notes are exempt. A promoted note's title ends in its state — "… [rejected]"
// — and the state is what every one-line surface reads it for, so cutting the
// tail would drop exactly the part that matters and, worse, make a state
// change invisible to the comparison that decides whether to rewrite the row
// (#R11).
//
// What makes the exemption safe is not that the text is deja's own — the notes
// file is edited by hand, so a note title is whatever someone typed (#2063) —
// but that every reader clips it for its own layout: `last`, `stats`, the view
// page and the MCP payloads through SafeNoteTitle, hook-context through
// trimBriefTitle. TestEverySurfaceThatPrintsANoteTitleBoundsIt holds that, and
// a new surface has to join it (#2092).
func boundSourceTitle(harness, title string) string {
	if harness == "deja" {
		return title
	}
	return truncateTitle(title, 60)
}

func truncateTitle(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
