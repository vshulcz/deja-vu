package index

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A rejected approach is the most expensive thing a store can forget: the next
// session re-implements it, tests it, watches it fail and reverts it again —
// call it ten thousand tokens. deja already models the state (`promote --state
// rejected`), and on a real store of 1160 sessions the count of sessions
// carrying it is zero, because it is set by hand and nobody sets it by hand.
//
// The evidence is there in the transcripts regardless: people say in so many
// words that they reverted, rolled back, or gave up on an approach. This
// detects that class of statement at ingest — a reversal, not a description of
// failure — so a hit can carry the fact. On a real 1165-session store it marks
// about 2% of sessions. It says nothing about which approach was dropped — that
// is what the excerpts are for — and it is not a lifecycle state: a state is
// the user's own judgement, and this is only what the transcript reports.
const (
	giveUpLineMin = 12
	giveUpLineMax = 300
)

// giveUpPhrases are the ways people say they backed something out. Kept to
// reports of a reversal — the decision to undo — and deliberately not to
// descriptions of failure. "didn't work", "does not work", "failed", "broken"
// are how nearly every debugging session opens ("the retry doesn't work when
// the queue is full") and how an assistant hedges ("if that doesn't work, we
// can add an index"); matching them marked half of ordinary sessions as dead
// ends. A reversal is a narrower, verifiable event: something was in, and was
// taken back out.
var giveUpPhrases = []string{
	// English — reversals only.
	"reverted", "rolled back", "rolling back", "roll it back",
	"backed out", "back it out", "gave up on", "giving up on",
	"abandoned that", "abandoned this", "abandoned the",
	"dropped that approach", "dropped this approach", "scrap that",
	"undid the", "undo the change", "reverting the",
	// Russian — reversals only.
	"откатил", "откатили", "откатываю", "откатывать",
	"вернул как было", "вернули как было", "вернул обратно", "вернули обратно",
	"отказались от", "отказался от", "отбросил", "отменил изменен",
}

// GiveUpLine reports whether a line says an approach was tried and dropped.
func GiveUpLine(l string) (string, bool) {
	l = strings.TrimSpace(l)
	if len(l) < giveUpLineMin || len(l) > giveUpLineMax {
		return l, false
	}
	// Source, not speech: a string literal, a comment or a diff line that
	// contains the words is code about reverting, not a report of one.
	for _, source := range []string{"\"", "$(", "=~", "print(", "//", "#", "/*"} {
		if strings.HasPrefix(l, source) || strings.Contains(l, source) {
			return l, false
		}
	}
	low := strings.ToLower(l)
	// A question is not a report. "should we revert this?" is the moment
	// before the decision, and marking it would put the label on the session
	// that considered reverting rather than the one that did.
	if strings.HasSuffix(low, "?") {
		return l, false
	}
	for _, p := range giveUpPhrases {
		if strings.Contains(low, p) {
			return l, true
		}
	}
	return l, false
}

// gaveUpFromRecords is gaveUp for the import path, which holds a session as
// records rather than as messages.
func gaveUpFromRecords(recs []Record) bool {
	for _, r := range recs {
		if r.Role != "user" && r.Role != "assistant" {
			continue
		}
		for _, line := range strings.Split(r.Text, "\n") {
			if _, ok := GiveUpLine(line); ok {
				return true
			}
		}
	}
	return false
}

// gaveUp reports whether a session says, somewhere in what was actually said,
// that something was tried and dropped. Only speech counts: tool output is
// full of the word "reverted" from git itself, and a command someone ran is
// not a statement about how it went.
func gaveUp(ms []model.Message) bool {
	for _, m := range ms {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		for _, line := range strings.Split(m.Text, "\n") {
			if _, ok := GiveUpLine(line); ok {
				return true
			}
		}
	}
	return false
}
