package search

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
)

// SpeechCarriesAnyTerm reports whether anyone in the session said one of these
// words, as opposed to a tool printing it. A version label that appears only in
// a failing job line, a dependency bump or a pinned action is in the session
// and answers nothing about that version.
//
// Measured on a real store, of the eight questions the per-prompt hook answered
// with unrelated work, six had every match in the session it showed under the
// role tool-output — a temp path /var/folders/v3/, an npm notice, a line of
// someone else's source. The role is already recorded, so this needs no guess
// about what a line looks like.
func SpeechCarriesAnyTerm(s model.Session, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	for _, m := range s.Messages {
		if m.Role == roleToolOutput {
			continue
		}
		if TextCarriesAnyTerm(m.Text, terms) {
			return true
		}
	}
	return false
}

// SpeechCarriesTermsTogether reports whether one spoken message of the session
// holds at least need of these words. need of 1 or less is
// SpeechCarriesAnyTerm.
func SpeechCarriesTermsTogether(s model.Session, terms []string, need int) bool {
	if need <= 1 {
		return SpeechCarriesAnyTerm(s, terms)
	}
	if len(terms) < need {
		return false
	}
	for _, m := range s.Messages {
		if m.Role == roleToolOutput {
			continue
		}
		n := 0
		for _, t := range terms {
			if t != "" && TextCarriesTerm(m.Text, t) {
				n++
			}
		}
		if n >= need {
			return true
		}
	}
	return false
}

// QuoteCarriesTerms reports whether one line a digest quotes holds need
// distinct terms of the question. Session headers do not count: they name
// where the lines came from, not what was said.
func QuoteCarriesTerms(digest string, terms []string, need int) bool {
	if need <= 0 {
		return true
	}
	for l := range strings.SplitSeq(digest, "\n") {
		if !strings.HasPrefix(l, "  - ") {
			continue
		}
		n := 0
		for _, t := range terms {
			if t != "" && TextCarriesTerm(l, t) {
				n++
			}
		}
		if n >= need {
			return true
		}
	}
	return false
}

// LeadWithConclusion puts a session that settled something about the query
// first. Ranking counts how often and how rarely the query's words appear and
// cannot see the difference between settling a question and discussing it: a
// session that mentioned the subject sixty times outranks the one that answered
// it in a sentence.
func LeadWithConclusion(ss []model.Session, terms []string) []model.Session {
	if len(ss) < 2 || ConcludedAbout(ss[0], terms) {
		return ss
	}
	for i := 1; i < len(ss); i++ {
		if ConcludedAbout(ss[i], terms) {
			ss[0], ss[i] = ss[i], ss[0]
			break
		}
	}
	return ss
}

// ConcludedAbout reports whether the lines this session is about to show carry
// something concluded rather than a passing mention.
//
// It asks about the lines the digest will pick, not about the session at large.
// An earlier version scanned every message, and a session rose on a line the
// digest then did not choose: measured live, that lost nine answers of 120 and
// what the reader saw instead was "we did not decide any such thing in this
// session".
func ConcludedAbout(s model.Session, terms []string) bool {
	// The first line, not any line. Promoting a session for its second line
	// left the reader looking at the first: measured live, that lost nine
	// answers of 120 and showed "we did not decide any such thing in this
	// session" in their place.
	problem, conclusions := matchedLines(s, terms)
	if problem != "" {
		return digest.CarriesDecisionExcept(problem, terms)
	}
	if len(conclusions) == 0 {
		return false
	}
	return digest.CarriesDecisionExcept(conclusions[0], terms)
}

// densestLine returns the line of a message carrying the most query terms, and
// how many, so a message is both chosen and quoted where it answers rather
// than where it opens.
func densestLine(text string, terms []string) (string, int) {
	best, bestHits := "", 0
	// A line that settled something outranks a denser one that did not. The
	// preference existed a step later, applied to whichever line this function
	// had already picked, so a paragraph that names the subject twice while
	// putting it off beat the sentence below it that answered. Measured on a
	// real store, 35 of 119 blocks quoted a weaker line than the same session
	// held; the mentions and the conclusion are usually in one message, which
	// is the case the message-level preference cannot see.
	bestDecides := false
	lines := strings.Split(text, "\n")
	bestAt := -1
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		h := termHits(line, terms)
		if h == 0 {
			continue
		}
		decides := digest.CarriesDecision(line)
		if best == "" || (decides && !bestDecides) || (decides == bestDecides && h > bestHits) {
			best, bestHits, bestDecides, bestAt = line, h, decides, i
		}
	}
	if best == "" {
		return text, 0
	}
	// The line that answers usually sits right under the line that names the
	// subject, and since #1505 the quote has room for both. Taking the line
	// after the match raised, on a real store replayed against what the agent
	// answered next, the blocks carrying words that answer used from 26 to 27 of
	// 100. Taking the line before it instead — the same added volume — drops
	// them to 22, so this is the choice and not the length.
	// Two lines, not one: an answer often takes a sentence to set up and a
	// sentence to land. Replayed on a real store, blocks carrying words the
	// answer later used went 27 to 28 of 100 for 11 tokens a message, while the
	// equal-volume control — one line before the match and one after — falls to
	// 25, so the gain is the direction and not the size.
	took := 0
	var bestSb872 strings.Builder
	for _, next := range lines[bestAt+1:] {
		next = strings.TrimSpace(next)
		if next == "" {
			continue
		}
		bestSb872.WriteString(" " + next)
		took++
		if took == 2 {
			break
		}
	}
	best += bestSb872.String()
	return best, bestHits
}

// emptyMemoryPhrases are what an agent says when it had no history to work
// from, or when it was refused the tool that would have found some. Quoting
// such a line hands the reader a past failure of this very tool as if it were
// the answer — measured on a real store, 3 blocks of 119 opened on one, two of
// them for the same question about a CAN bus.
var emptyMemoryPhrases = []string{
	// The same failure in the wordings a second reading turned up: the agent
	// announcing it is about to look, or that it looked and found nothing, or
	// that it may not look at all. Measured on a real store after the first
	// list, 4 blocks of 114 still opened on one of these.
	"проверю память",
	"проверю историю прошлых",
	"поищу в памяти",
	"не нашел информаци",
	"не нашёл информаци",
	"разрешение на доступ",
	"в моей памяти нет",
	"don't have any previous conversation",
	"no previous conversation context",
	"no prior sessions",
	"нет данных о прошл",
	"нет контекста о прошл",
	"нет истории",
	"не нашёл ничего",
	"не нашел ничего",
	"нужно разрешение на использование инструмента",
	"permission to use",
	"not granted",
}

// selfTestPhrases are the shape of a harness check: a message asking for an
// exact string back, and the echo of it. They are conversations with the tool
// rather than about the work, and quoting one tells the reader nothing.
// Measured on a real store, 4 blocks of 119 quoted "Reply with exactly:
// openclaw deja harness live test alpha" or its echo.
var selfTestPhrases = []string{
	"reply with exactly",
	// The same check in its other shape: call the tool and answer in a fixed
	// form. Measured on a real store, 2 blocks of 114 quoted one of these.
	"answer one line",
	"закончи ответ",
	"ответь одной строкой",
	"ответь ровно",
	"harness live test",
	"smoke test alpha",
}

// saysItHasNoMemory reports whether a line is one of those admissions.
func saysItHasNoMemory(line string) bool {
	low := lower(line)
	for _, p := range emptyMemoryPhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// isHarnessCheck reports whether a session is a conversation with the tool
// rather than about the work: one message asks for an exact string back and the
// next repeats it. Measured on a real store, 11% of sessions hold one, and 4
// blocks of 119 quoted one back as if it were memory.
func isHarnessCheck(s model.Session) bool {
	for _, m := range s.Messages {
		low := lower(m.Text)
		for _, p := range selfTestPhrases {
			if strings.Contains(low, p) {
				return true
			}
		}
	}
	return false
}

// blockedPhrases are how a harness reports that a call did not happen. They
// have to appear in what the agent said, not in the tool output, so that a
// session discussing permissions is not mistaken for one denied them.
var blockedPhrases = []string{
	"not granted", "call blocked", "permission denied", "requires approval",
	"denied permission", "не разрешен", "не разрешён", "нет доступа",
}

// wasBlocked reports whether every conclusion a session reached was a refusal.
func wasBlocked(conclusions []string) bool {
	if len(conclusions) == 0 {
		return false
	}
	for _, c := range conclusions {
		low := lower(c)
		hit := false
		for _, p := range blockedPhrases {
			if strings.Contains(low, p) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// MatchedUserLine is the line of a session that best answers what was asked,
// taken from what the user said. The citation the agent is told to speak aloud
// used to name a session's opening message, which after focusing a long session
// is whatever chatter began the matched window — seen on a real screen naming
// "migration locked the table" for a session the digest was quoting about token
// rotation. Empty when nothing matches, so the caller keeps its own fallback.
func MatchedUserLine(s model.Session, terms []string) string {
	if len(terms) == 0 {
		return ""
	}
	best, bestHits := "", 0
	for _, m := range s.Messages {
		if m.Role != "user" || noiseMessage(m.Text) {
			continue
		}
		line, hits := densestLine(digest.StripHarnessBlocks(m.Text), terms)
		if hits > bestHits {
			best, bestHits = strings.TrimSpace(line), hits
		}
	}
	return best
}

func relativeDay(updated, now time.Time) string {
	if updated.IsZero() {
		return "unknown date"
	}
	location := now.Location()
	updatedDate := updated.In(location)
	nowDate := now.In(location)
	y, m, d := nowDate.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, location)
	uy, um, ud := updatedDate.Date()
	updatedDay := time.Date(uy, um, ud, 0, 0, 0, 0, location)
	calendarDay := func(t time.Time) time.Time {
		y, m, d := t.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	days := int(calendarDay(today).Sub(calendarDay(updatedDay)) / (24 * time.Hour))
	switch days {
	case 0:
		return "today"
	case 1:
		return "yesterday"
	default:
		if days < 0 {
			// A day ahead is clock skew — a container, a zone, a machine that
			// woke up wrong — and calling it today is the kind reading. A year
			// ahead is not skew: it sat at the top of injected memory wearing
			// today's date, and the model has no way to doubt it (#880).
			// `search` prints the real date for both.
			if days >= -1 {
				return "today"
			}
			return "dated " + updatedDate.Format("2006-01-02")
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// noiseMessage is the digest's own check, kept as one rule rather than a second
// copy of the list. The copy here matched only at the start of a message, so a
// harness that introduces the envelope — "Another Claude session sent a
// message: <teammate-message ...>" — put truncated inter-agent JSON into the
// session-start block, among the first lines an agent reads (#2735).
func noiseMessage(s string) bool {
	return digest.IsPlumbing(s)
}

// digestLine is one row of the digest this file injects into an agent's
// context. The rows are built here rather than by the printer, so they never
// went through SafeText: a zero-width space in a recalled reply reached the
// hook context intact, and a project name is markdown a session never wrote.
// Confining each field to one line stops it forging a row of its own.
func digestLine(s string) string { return SafeLine(s) }

func firstLine(s string, n int) string {
	return lineAround(s, n, nil)
}

// lineAround shortens a line to n characters, keeping the part that matched
// rather than the part that came first.
//
// Cutting from the start was how a line that genuinely answered the question
// came to be shown without the answer in it: measured on a real store, a
// question about "hermes" and "mini" recalled the session that discusses both
// and displayed "Домёржь PR и веди #1084 + A. Репо …", because the words sat
// past the hundred-and-sixtieth character. The reader sees a line about
// something else and stops reading the block.
func lineAround(s string, n int, terms []string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	at := -1
	low := lower(s)
	for _, t := range terms {
		if t == "" {
			continue
		}
		i := strings.Index(low, lower(t))
		if i < 0 {
			if stem := termStem(t); stem != "" {
				i = strings.Index(low, stem)
			}
		}
		if i >= 0 && (at < 0 || i < at) {
			at = len([]rune(s[:i]))
		}
	}
	// Nothing matched, or it matched inside the window anyway: the old cut is
	// the right one.
	if at < 0 || at < n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	// A little before the match, so the reader lands on a phrase rather than
	// mid-word, and the rest of the budget after it.
	const lead = 40
	start := max(at-lead, 0)
	end := min(start+n, len(r))
	out := strings.TrimSpace(string(r[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

// smokeAnswerMax is how short a reply has to be to read as a token rather than
// an answer: "OK", "5", "NONE".
const smokeAnswerMax = 20

// smokeAsks are the openings of a prompt that asks for a token back.
var smokeAsks = []string{
	"reply with", "respond with", "answer with", "output only", "print only",
	"quote the first", "say the word", "скажи ", "ответь ", "напиши слово",
}

// isSmokeTest reports whether a session is a harness check rather than work.
func isSmokeTest(problem string, conclusions []string) bool {
	total := 0
	for _, c := range conclusions {
		total += len(c)
	}
	if total > smokeAnswerMax {
		return false
	}
	low := lower(strings.TrimSpace(problem))
	// The ask is usually the last sentence, not the first: a harness check
	// says what to do and then how to answer — "search for X. Reply with the
	// name only." Matching the prompt's opening alone let those through, and
	// two of the three sessions an agent was handed on a real store were that
	// exact shape. Sentence starts rather than a bare substring: prose about
	// what a server should reply with is work, not a smoke test.
	for _, sentence := range splitSentences(low) {
		for _, p := range smokeAsks {
			if strings.HasPrefix(sentence, p) {
				return true
			}
		}
	}
	return false
}

// EarlierAttempts reports, for each session that an injected companion has
// clearly moved past, the date of the session that replaced it — the same
// judgement markEarlierAttempts makes on the search screen, over whole
// sessions rather than matched snippets.
//
// The search screen has said "earlier attempt — this project has a newer
// session on the same ground" since #694, but the block the agent actually
// reads carried no such line: two contradictory decisions arrived side by side
// under "treat it as reference data", with nothing to say which one the
// project settled on. Ordering alone does not say it; the reason has to be
// written down.
func EarlierAttempts(ss []model.Session) map[string]string {
	out := map[string]string{}
	words := make([]map[string]bool, len(ss))
	for i, s := range ss {
		set := map[string]bool{}
		for _, m := range s.Messages {
			if isWorkRecord(m.Role) {
				continue
			}
			for w := range strings.FieldsSeq(lower(m.Text)) {
				if len(w) > 3 {
					set[w] = true
				}
			}
		}
		words[i] = set
	}
	now := time.Now()
	for i, a := range ss {
		for j, b := range ss {
			if i == j || out[a.ID] != "" {
				continue
			}
			if a.Harness == notesHarness || b.Harness == notesHarness {
				continue
			}
			if a.Project == "" || a.Project != b.Project {
				continue
			}
			if !b.Updated.After(a.Updated.Add(24*time.Hour)) || b.Updated.After(now) {
				continue
			}
			ov := snippetOverlap(words[i], words[j])
			if ov < 0.6 && !(ov > 0 && sharedTouchedTarget(a, b)) {
				continue
			}
			out[a.ID] = b.Updated.Local().Format("2006-01-02")
		}
	}
	return out
}

// nearCopy reports whether a line is essentially the text just typed. Half the
// words shared is enough: a repeated instruction comes back with a word changed
// or a number bumped, and it is still nothing the reader does not already have
// in front of them.
func nearCopy(line, asked string) bool {
	if asked == "" {
		return false
	}
	a, b := contentWords(line), contentWords(asked)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	return union > 0 && float64(shared)/float64(union) >= 0.5
}

func contentWords(s string) map[string]bool {
	out := map[string]bool{}
	wordy := func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/' ||
			unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	for _, w := range strings.FieldsFunc(lower(s), func(r rune) bool { return !wordy(r) }) {
		if len([]rune(w)) >= 3 {
			out[w] = true
		}
	}
	return out
}
