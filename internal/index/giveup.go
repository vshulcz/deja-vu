package index

import (
	"strings"
	"unicode/utf8"

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
	// Length in runes, not bytes: a byte budget gives Cyrillic half the room,
	// so a Russian report of a rollback was rejected as too long at 150 bytes
	// (75 characters) and the 12-byte floor was only six Cyrillic characters.
	if n := utf8.RuneCountInString(l); n < giveUpLineMin || n > giveUpLineMax {
		return l, false
	}
	// Source, not speech: a line that IS code — a comment, a string literal, a
	// diff line — is talking about reverting, not reporting it. But the marker
	// only ever runs on user/assistant prose, and real prose carries quotes,
	// issue refs and URLs ("reverted the change from #1143", "rolled back after
	// reading github.com/x/y//issues"). So reject a line that BEGINS like code,
	// not any line that merely contains a quote or a slash somewhere.
	for _, prefix := range []string{"//", "#", "/*", "*", "--", "-- ", ">", "|"} {
		if strings.HasPrefix(l, prefix) {
			return l, false
		}
	}
	// A function call with a string argument is code, wherever it sits:
	// `log.Printf("reverted %s", name)`. `("` is the call-with-string shape and
	// does not appear in prose, whereas a bare quote (`reverted the "fast path"`)
	// does — so this rejects the code without rejecting the report.
	for _, code := range []string{"(\"", "('", "=~", "println", "printf(", "system("} {
		if strings.Contains(l, code) {
			return l, false
		}
	}
	low := strings.ToLower(l)
	// A pure question is the moment before the decision, not the decision:
	// "should we revert this?" must not mark the session that only considered
	// it. But a report that ends with a follow-up question is still a report —
	// "откатили, но не помогло, что дальше?" — so only reject a line that is
	// nothing but a question: it ends in "?" and no reversal phrase precedes.
	isQuestion := strings.HasSuffix(strings.TrimSpace(low), "?")
	for _, p := range giveUpPhrases {
		if strings.Contains(low, p) {
			if isQuestion && phraseInQuestionClause(low, p) {
				return l, false
			}
			return l, true
		}
	}
	return l, false
}

// phraseInQuestionClause reports whether the reversal phrase shares the final
// interrogative clause with the trailing "?" — "should we have reverted this?"
// — rather than sitting in a statement before it — "откатили, но не помогло,
// что дальше?". The test is whether any sentence boundary separates the phrase
// from the question mark: none means the phrase is part of the question.
func phraseInQuestionClause(low, phrase string) bool {
	i := strings.Index(low, phrase)
	if i < 0 {
		return true
	}
	tail := low[i+len(phrase):]
	for _, sep := range []string{". ", "! ", "? ", "— ", " - ", ", ", "; "} {
		if strings.Contains(tail, sep) {
			return false
		}
	}
	return true
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
