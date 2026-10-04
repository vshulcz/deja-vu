package digest

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// thinSentence reports whether a sentence carries nothing a later session could
// use on its own: a question (it concludes nothing), a lead-in ending in a colon
// (the content is what follows it), an announcement of the next step, or a
// short line that names nothing.
//
// Short is the bar resume.go already holds its decisions to. The quoted lines
// that failed a hand check on real session starts were all under it — "Fixed
// the same way.", "работает", "Итерация 258.", "It printed: `hi`" — and the
// short ones that held up name something: "`toolHookMaxBytes` (480 bytes).",
// "We chose a 32-byte nonce."
func thinSentence(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	plain := strings.TrimSpace(strings.Trim(s, "*_#>-•· \t"))
	if strings.HasSuffix(plain, "?") || strings.HasSuffix(plain, "？") ||
		strings.HasSuffix(plain, ":") || strings.HasSuffix(plain, "：") {
		return true
	}
	// "Проверяю контроль из #4214", "I'll run `echo hi` and report the output":
	// what the speaker was about to do, by the same openers the decision rule
	// already refuses to read as outcomes. Except "сейчас", which opens a state
	// as often as a plan — "Сейчас Hermes — это «читалка»: 15 кронов…" was
	// dropped on a real recall page.
	if low := strings.ToLower(plain); announcesItself(low) && !strings.HasPrefix(low, "сейчас ") {
		return true
	}
	if namesSomething(s) || namesWhatWasDecided(strings.ToLower(plain)) {
		return false
	}
	return utf8.RuneCountInString(plain) < decisionMinRunes
}

// namesWhatWasDecided reports whether a short line says what its decision was
// about: a marker with something in front of it. "vendoring fixed it" does;
// "Fixed the same way.", "Merged." and "Причина найдена." open on the marker
// and leave the what to a message the reader will not see.
func namesWhatWasDecided(low string) bool {
	for _, d := range decisionMarkers {
		if i := strings.Index(low, d); i > 0 && marksLine(low, d) {
			return true
		}
	}
	return false
}

// namesSomething reports whether a line carries a concrete token: code of a
// few characters, a path, a flag, an assignment, an issue number, or a word
// mixing letters and digits like "v1.2" or "32-byte". A bare number does not
// count — "Итерация 258." names a step, not what happened in it.
func namesSomething(s string) bool {
	for rest := s; ; {
		i := strings.IndexByte(rest, '`')
		if i < 0 {
			break
		}
		j := strings.IndexByte(rest[i+1:], '`')
		if j < 0 {
			break
		}
		if utf8.RuneCountInString(strings.TrimSpace(rest[i+1:i+1+j])) >= 3 {
			return true
		}
		rest = rest[i+1+j+1:]
	}
	for _, tok := range strings.Fields(s) {
		tok = strings.Trim(tok, ".,;:!?()[]«»\"'*`")
		if tok == "" {
			continue
		}
		if strings.ContainsAny(tok, "/_=@") || strings.Contains(tok, "--") {
			return true
		}
		if tok[0] == '#' && len(tok) > 1 && tok[1] >= '0' && tok[1] <= '9' {
			return true
		}
		letter, digit := false, false
		for _, r := range tok {
			switch {
			case unicode.IsLetter(r):
				letter = true
			case unicode.IsDigit(r):
				digit = true
			}
		}
		if letter && digit {
			return true
		}
	}
	return false
}

// listMarker reports whether a would-be sentence is only the number that opens
// a list item: "Found 3 production bugs. 1. **High — …**" ended its two
// sentences at "1.", and the block quoted the count with the bug cut off.
func listMarker(sentence string) bool {
	t := strings.TrimSpace(sentence)
	if !strings.HasSuffix(t, ".") || len(t) < 2 || len(t) > 4 {
		return false
	}
	for _, r := range t[:len(t)-1] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// substantialLead is the lead decisionLead would quote, moved past openers that
// say nothing. When every sentence of the default head is thin — "Готово.
// Полный отчёт ниже.", "Проверил." — the quote starts at the first sentence that
// carries something and keeps the usual two. A message with no such sentence
// gives nothing back, so the caller moves on to an older conclusion instead of
// quoting a greeting or a status stub.
func substantialLead(text, head string) string {
	if head == "" {
		return ""
	}
	sents := sentencesOf(head)
	for _, s := range sents {
		if !thinSentence(s) {
			return head
		}
	}
	all := sentencesOf(text)
	for i, s := range all {
		if thinSentence(s) {
			continue
		}
		end := i + 2
		if end > len(all) {
			end = len(all)
		}
		return strings.Join(all[i:end], " ")
	}
	return ""
}

// SaysSomething reports whether a message holds at least one sentence a reader
// could use without the rest of the session — the bar SubstantialConclusions
// holds its lines to.
func SaysSomething(text string) bool {
	for _, s := range sentencesOf(text) {
		if !thinSentence(s) {
			return true
		}
	}
	return false
}

// firstSubstantialSentence is the one-sentence cut of a lead that would not fit
// the budget: the first sentence that carries something, not merely the first.
// Cutting to the opening sentence turned "Итерация 258. <what it did>" into
// "Итерация 258." on a real session start.
func firstSubstantialSentence(line string) string {
	sents := sentencesOf(line)
	for i, s := range sents {
		if !thinSentence(s) {
			// Through firstSentences, so the cut keeps its cap on a line with
			// no sentence end at all.
			return firstSentences(strings.Join(sents[i:], " "), 1)
		}
	}
	return ""
}
