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
	"отказались от", "отказался от", "отбросили", "отбросил подход", "отбросил вариант", "отбросил идею", "отменил изменен",
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
	// An assistant states a reversal inside a bulleted or quoted summary as
	// often as in a sentence ("* reverted the caching layer, it broke
	// ordering"). Strip a leading list or quote marker so the report inside is
	// still read. Only "* " and "> ", which are unambiguous — "- " and "+ " are
	// also diff markers, and a removed line "- reverted the old code" is not a
	// report of anyone reverting anything.
	for _, bullet := range []string{"* ", "> "} {
		if strings.HasPrefix(l, bullet) {
			l = strings.TrimSpace(l[len(bullet):])
			break
		}
	}
	// Source, not speech: a line that IS code — a comment, a string literal, a
	// diff line — is talking about reverting, not reporting it. But the marker
	// only ever runs on user/assistant prose, and real prose carries quotes,
	// issue refs and URLs ("reverted the change from #1143", "rolled back after
	// reading github.com/x/y//issues"). So reject a line that BEGINS like code,
	// not any line that merely contains a quote or a slash somewhere.
	for _, prefix := range []string{"//", "#", "/*", "--", "- ", "+ ", "|"} {
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
	if isTestVerificationOrHypotheticalReversal(low) {
		return l, false
	}
	for _, p := range giveUpPhrases {
		i := strings.Index(low, p)
		if i < 0 {
			continue
		}
		// A reflexive Russian verb is a thing rolling by itself, not someone
		// rolling a change back: "откатил" is a substring of "откатился" ("the
		// ball rolled under the couch"). The reflexive particle "-ся"/"-сь"
		// comes after the gender/number vowel — откатил·ся, откатил·а·сь,
		// откатил·о·сь, откатил·и·сь — so check for it with the vowel optional.
		if reflexiveSuffix(low[i+len(p):]) {
			continue
		}
		if phraseQuotedOrCode(low, i, len(p)) {
			continue
		}
		clause := reversalClause(low, i, len(p))
		// A reversal that did not happen is not a report. Negation ("we
		// haven't reverted", "never gave up on", "не откатывать"), a
		// conditional or future ("if it fails we roll it back", "I'll be
		// reverting tomorrow"), and a bare question ("should we have reverted
		// this?") all describe a reversal that was considered, refused, or is
		// still ahead — not one that was done.
		if clauseNegatesOrDefers(clause, p) {
			continue
		}
		return l, true
	}
	return l, false
}

// reversalClause returns the sentence the phrase sits in, lowercased: from the
// separator before it to the separator after. The verdict — did this reversal
// happen — is a property of the clause, not the whole line, so a report in one
// sentence is not overruled by a question in the next.
func reversalClause(low string, phraseAt, phraseLen int) string {
	seps := []string{". ", "! ", "? ", "; ", "— ", " - ", ", "}
	start := 0
	for _, sep := range seps {
		if j := strings.LastIndex(low[:phraseAt], sep); j >= 0 && j+len(sep) > start {
			start = j + len(sep)
		}
	}
	end := len(low)
	after := phraseAt + phraseLen
	for _, sep := range seps {
		if j := strings.Index(low[after:], sep); j >= 0 && after+j < end {
			end = after + j
		}
	}
	return low[start:end]
}

// reflexiveSuffix reports whether the text right after a Russian reversal verb
// is a reflexive particle — with the gender/number vowel that precedes it in
// the past tense optional: ся, сь, ась, ось, ись, ялись, and so on.
func reflexiveSuffix(rest string) bool {
	for _, s := range []string{"ся", "сь", "ась", "ось", "ись", "лся", "лась", "лось", "лись"} {
		if strings.HasPrefix(rest, s) {
			return true
		}
	}
	return false
}

// phraseQuotedOrCode reports whether the matched reversal phrase is enclosed in
// backticks or quotation marks as a metalinguistic token rather than used as a
// statement of action.
func phraseQuotedOrCode(low string, phraseAt, phraseLen int) bool {
	if phraseAt <= 0 {
		return false
	}
	before := low[:phraseAt]
	after := low[phraseAt+phraseLen:]
	for _, q := range []string{"`", `"`, "'", "«", "“"} {
		if strings.HasSuffix(before, q) {
			return true
		}
	}
	for _, q := range []string{"`", `"`, "'", "»", "”"} {
		if strings.HasPrefix(after, q) {
			return true
		}
	}
	return false
}

// clauseNegatesOrDefers reports whether the clause holding the reversal phrase
// says the reversal did not happen: a negator or a conditional/future modal
// that governs the phrase (both must sit BEFORE it — "if we roll it back", not
// "reverted it since it would break"), or the clause being interrogative.
func clauseNegatesOrDefers(clause, phrase string) bool {
	pi := strings.Index(clause, phrase)
	// A negator or modal only governs the reversal when it comes before it. A
	// modal after the phrase belongs to a different verb — "reverted it since
	// it would corrupt state" is a report, not a hypothetical.
	for _, w := range []string{
		"not ", "n't ", "never ", "without ", "avoid ",
		"if ", "would ", "could ", "should ", "might ", "planning to ",
		"going to ", "i'll ", "we'll ", "will ",
		"не ", "нельзя ", "без ", "если ", "надо ли ", "стоит ли ",
	} {
		if j := strings.Index(clause, w); j >= 0 && j < pi {
			return true
		}
	}
	// The clause itself is the question — "did we roll it back, or is it still
	// live?" — even when the "?" lands in a later clause. Strong question words
	// only ever lead a question.
	c := strings.TrimSpace(clause)
	for _, q := range []string{"did ", "do ", "does ", "why ", "what ", "how ", "when ", "whether ", "can "} {
		if strings.HasPrefix(c, q) {
			return true
		}
	}
	// A copula (is/was/are/were) leads both a passive report ("was rolled back
	// after review") and a question ("was that rolled back"). The tell is what
	// follows it: a passive report puts the reversal phrase straight after the
	// copula, a question inserts a subject ("that", "this", "those") first.
	for _, cop := range []string{"is ", "are ", "was ", "were "} {
		if strings.HasPrefix(c, cop) && !strings.HasPrefix(c, cop+phrase) {
			return true
		}
	}
	return strings.HasSuffix(c, "?")
}

// isTestVerificationOrHypotheticalReversal rejects temporary scratch/hunk
// rollbacks done solely to prove a regression test fails ("with the hunk
// reverted, TestX fails") and hypothetical/meta discussions of reversals.
func isTestVerificationOrHypotheticalReversal(low string) bool {
	for _, pat := range []string{
		"hunk reverted",
		"fix reverted",
		"fix is reverted",
		"reverting the fix",
		"reverting the line",
		"reverted copy",
		"reverted tree",
		"reverted to commit",
		"reverted probes",
		"scratch copy",
		"scratchpad copy",
		"scratchpad",
		"verified: with ",
		"verified with ",
		"would still be",
		"telling the agent",
		"tell the agent",
		"test is sound",
		"fail-before confirmed",
		"necessity confirmed",
		"откатывать внутри копии",
		"откатывать хуже",
	} {
		if strings.Contains(low, pat) {
			return true
		}
	}
	if h := strings.Index(low, "reverting the "); h >= 0 && strings.Contains(low[h:], " hunk") {
		return true
	}
	if w := strings.Index(low, "with "); w >= 0 {
		rest := low[w:]
		if r := strings.Index(rest, " reverted"); r >= 0 && (strings.Contains(rest[r:], " fail") || strings.Contains(rest[r:], " fails")) {
			return true
		}
	}
	return false
}

// GiveUpExcerpts returns reversal-bearing prose excerpts from text. Lines
// within giveUpLineMax runes are checked directly via GiveUpLine; longer
// paragraphs are split into backtick-safe sentences and checked as adjacent
// 2-sentence windows (preserving the target subject + causal reason alongside
// the rollback statement) before falling back to individual sentences.
func GiveUpExcerpts(text string) []string {
	var out []string
	inFence := false
	for rawLine := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" {
			continue
		}
		if l, ok := GiveUpLine(trimmed); ok {
			out = append(out, l)
			continue
		}
		if utf8.RuneCountInString(trimmed) <= giveUpLineMax {
			continue
		}
		sents := splitCausalCandidates(trimmed)
		for i := range sents {
			if i > 0 {
				pair := sents[i-1] + " " + sents[i]
				if l, ok := GiveUpLine(pair); ok {
					out = append(out, l)
					continue
				}
			}
			if i+1 < len(sents) {
				pair := sents[i] + " " + sents[i+1]
				if l, ok := GiveUpLine(pair); ok {
					out = append(out, l)
					continue
				}
			}
			if l, ok := GiveUpLine(sents[i]); ok {
				out = append(out, l)
			}
		}
	}
	return out
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
		if len(GiveUpExcerpts(m.Text)) > 0 {
			return true
		}
	}
	return false
}
