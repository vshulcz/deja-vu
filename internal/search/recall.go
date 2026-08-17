package search

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	RecallOff        = "off"
	RecallSafe       = "safe"
	RecallAggressive = "aggressive"
)

type AutoRecallOptions struct {
	Mode         string
	ProjectNames []string
	Now          time.Time
	// TaskScores ranks sessions by overlap with what the repo is touching
	// right now (harness:ID → matched-file count). Sessions the task points
	// at outrank plain recency; zero or a nil map falls back to recency.
	TaskScores map[string]int
}

type AutoRecallResult struct {
	Text     string
	Sessions int
	// RawBytes is the transcript volume of the sessions that made it into
	// Text — the denominator of the distillation ratio deja prints as its
	// own measurement. Candidates the digest skipped never reached an agent,
	// so counting them inflates the ratio.
	RawBytes int64
}

func AutoRecallDigest(ss []model.Session, budget int) string {
	return AutoRecallDigestFor(ss, budget, nil)
}

// AutoRecallDigestFor renders the digest around the words that were asked
// about. Without terms it keeps the session-start behaviour, where there is no
// question yet and the opening of a session is the best summary of it.
//
// With terms it matters a great deal. A long session narrowed to the region
// that matched still holds hundreds of messages, and showing that region's
// first three lines answers a different question than the one asked: measured
// on a real 8608-message session, the block an agent received carried one or
// two of the four words it had searched for.
func AutoRecallDigestFor(ss []model.Session, budget int, terms []string) string {
	if budget <= 0 {
		budget = 2000
	}
	var b strings.Builder
	for _, s := range ss {
		if b.Len() >= budget {
			break
		}
		section := autoRecallSessionFor(s, time.Now(), false, terms)
		if section == "" {
			continue
		}
		if b.Len()+len(section) > budget {
			cut := budget - b.Len()
			for cut > 0 && !utf8.RuneStart(section[cut]) {
				cut--
			}
			section = section[:cut]
		}
		b.WriteString(section)
	}
	return strings.TrimSpace(b.String())
}

// BuildAutoRecall applies the session-start recall policy while constructing
// the digest. Unknown modes use the safe policy.
func BuildAutoRecall(ss []model.Session, o AutoRecallOptions) AutoRecallResult {
	mode := strings.ToLower(strings.TrimSpace(o.Mode))
	if mode == RecallOff {
		return AutoRecallResult{}
	}
	if mode != RecallAggressive {
		mode = RecallSafe
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	candidates := append([]model.Session(nil), ss...)
	sort.SliceStable(candidates, func(i, j int) bool {
		ti := o.TaskScores[candidates[i].Harness+":"+candidates[i].ID]
		tj := o.TaskScores[candidates[j].Harness+":"+candidates[j].ID]
		if ti != tj {
			return ti > tj
		}
		iRecent := !candidates[i].Updated.Before(o.Now.AddDate(0, 0, -90))
		jRecent := !candidates[j].Updated.Before(o.Now.AddDate(0, 0, -90))
		if iRecent != jRecent {
			return iRecent
		}
		return candidates[i].Updated.After(candidates[j].Updated)
	})

	budget := 4096
	maxSessions := 6
	if mode == RecallSafe {
		budget = 2048
		maxSessions = 3
	}
	var b strings.Builder
	var fingerprints []map[string]bool
	var raw int64
	for _, s := range candidates {
		if mode == RecallSafe && !projectMatches(s.Project, o.ProjectNames) {
			continue
		}
		section := autoRecallSession(s, o.Now, true)
		if section == "" || (mode == RecallSafe && relevanceWords(s) < 3) {
			continue
		}
		fingerprint := sessionWordSet(s)
		if mode == RecallSafe && nearDuplicate(fingerprint, fingerprints) {
			continue
		}
		if b.Len()+len(section) > budget {
			cut := budget - b.Len()
			for cut > 0 && !utf8.RuneStart(section[cut]) {
				cut--
			}
			section = section[:cut]
		}
		if section == "" {
			break
		}
		b.WriteString(section)
		fingerprints = append(fingerprints, fingerprint)
		for _, m := range s.Messages {
			raw += int64(len(m.Text))
		}
		if b.Len() >= budget || len(fingerprints) >= maxSessions {
			break
		}
	}
	return AutoRecallResult{Text: strings.TrimSpace(b.String()), Sessions: len(fingerprints), RawBytes: raw}
}

func projectMatches(project string, names []string) bool {
	project = strings.ToLower(filepathClean(project))
	// A session imported from a peer carries its project under an "imported:"
	// prefix but is still this project's history. Match the base name too, or the
	// session-start digest silently drops imported sessions the trust policy
	// (default local+imported) already allowed — while hook-prompt keeps them, so
	// the two auto-injections disagreed on the same store (#E-new33).
	bare := strings.TrimPrefix(project, "imported:")
	for _, name := range names {
		name = strings.ToLower(filepathClean(name))
		if name == "" {
			continue
		}
		if project == name || strings.HasSuffix(project, "/"+name) ||
			bare == name || strings.HasSuffix(bare, "/"+name) {
			return true
		}
	}
	return false
}

func filepathClean(s string) string {
	return strings.Trim(strings.ReplaceAll(s, "\\", "/"), "/")
}

func relevanceWords(s model.Session) int {
	return len(sessionWordSet(s))
}

func sessionWordSet(s model.Session) map[string]bool {
	text := s.Title
	for _, m := range s.Messages {
		text += " " + m.Text
	}
	return wordSet(text)
}

func wordSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(word) >= 3 {
			set[word] = true
		}
	}
	return set
}

func nearDuplicate(candidate map[string]bool, prior []map[string]bool) bool {
	for _, other := range prior {
		intersection := 0
		for word := range candidate {
			if other[word] {
				intersection++
			}
		}
		union := len(candidate) + len(other) - intersection
		if union > 0 && float64(intersection)/float64(union) >= 0.8 {
			return true
		}
	}
	return false
}

func autoRecallSession(s model.Session, now time.Time, provenance bool) string {
	return autoRecallSessionFor(s, now, provenance, nil)
}

// termHits counts how many distinct query terms a text carries. It decides
// which lines of a matched session are worth showing, so it folds case and
// nothing else: a term the ranking reached through a stem fold is not found
// here, which costs that line its place and never costs correctness.
func termHits(text string, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	low := strings.ToLower(text)
	n := 0
	for _, t := range terms {
		if t != "" && strings.Contains(low, strings.ToLower(t)) {
			n++
		}
	}
	return n
}

func autoRecallSessionFor(s model.Session, now time.Time, provenance bool, terms []string) string {
	var problem string
	var conclusions []string
	matched := false
	if len(terms) > 0 {
		problem, conclusions = matchedLines(s, terms)
		matched = len(conclusions) > 0
	} else {
		// No question yet — this is the block handed over at session start.
		// Nothing can be matched, and walking the transcript from the top takes
		// the first two things the agent said, which are "let me look" and "I
		// have found the file". What the session decided is at the end of it.
		//
		// digest.Conclusions already picks the decision-carrying lines, newest
		// first, and is what `deja share` prints under the same heading.
		conclusions = digest.Conclusions(s, 400, 2)
		matched = len(conclusions) > 0
	}
	for _, m := range s.Messages {
		if problem != "" && (len(conclusions) >= 2 || matched) {
			break
		}
		// One line that answers beats that line plus a filler. When the
		// session matched, the second slot is left empty rather than topped up
		// from the top of the session: on a real block that padding read as
		// "two schedulers were live after the failover" under an answer about
		// the retry budget.
		if matched && m.Role == "assistant" {
			continue
		}
		text := contextText(m.Text, false)
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch m.Role {
		case "user":
			if problem == "" && !noiseMessage(m.Text) {
				problem = firstLine(text, 160)
			}
		case "assistant":
			if len(conclusions) < 2 {
				conclusions = append(conclusions, firstLine(text, 220))
			}
		}
	}
	if problem == "" && len(conclusions) == 0 {
		return ""
	}
	// A harness smoke test is a real session and gets indexed like any other:
	// "Reply with the single word OK" → "OK". Injected into an agent's context
	// it teaches the reader that deja recalls worthless things.
	//
	// The test is deliberately narrow — a prompt asking for a token back,
	// answered with a token. An earlier version cut on total length instead and
	// dropped a one-line question with a short answer, which is memory worth
	// having.
	if isSmokeTest(problem, conclusions) {
		return ""
	}
	// A session where the agent only ever said it could not proceed carries
	// nothing to reuse. One had been sitting in a real store for twenty days,
	// blocked on a permission prompt, taking a slot in every agent's opening
	// context. This asks that *every* conclusion be a refusal, so work that
	// hit a wall and then got past it is untouched.
	if wasBlocked(conclusions) {
		return ""
	}
	var b strings.Builder
	if provenance {
		fmt.Fprintf(&b, "✓ recalled from %s session · %s\n", digestLine(s.Harness), relativeDay(s.Updated, now))
		fmt.Fprintf(&b, "  - Session: **%s** `%s`\n", digestLine(s.Project), digestLine(short(s.ID)))
	} else {
		date := ""
		if !s.Updated.IsZero() {
			date = " · " + s.Updated.Format("2006-01-02")
		}
		fmt.Fprintf(&b, "- **%s** `%s`%s\n", digestLine(s.Project), digestLine(short(s.ID)), date)
	}
	if problem != "" {
		fmt.Fprintf(&b, "  - User: %s\n", digestLine(problem))
	}
	for _, c := range conclusions {
		fmt.Fprintf(&b, "  - Assistant: %s\n", digestLine(c))
	}
	return b.String()
}

// matchedLines picks the lines of a session that carry what was asked about:
// the best-matching user message, and the two best-matching assistant
// messages in the order they were said. What matches nothing is left to the
// caller's fallback, so a session that ranked through a stem fold still shows
// something rather than nothing.
func matchedLines(s model.Session, terms []string) (string, []string) {
	type scored struct {
		idx, hits int
		text      string
	}
	var bestUser scored
	var assistants []scored
	for i, m := range s.Messages {
		// Score the raw text, not what contextText returns: it joins lines and
		// keeps only the first eight of them, so by then there is nothing left
		// to choose between and most of a long answer is already gone.
		//
		// Score the best single line rather than the whole message, too. A
		// compaction summary or a standing-constraints block mentions
		// everything the session ever touched, so counting terms across a
		// message hands the slot to the longest text rather than to the one
		// that answers.
		line, hits := densestLine(m.Text, terms)
		if hits == 0 {
			continue
		}
		text := contextText(line, false)
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch m.Role {
		case "user":
			if !noiseMessage(m.Text) && hits > bestUser.hits {
				bestUser = scored{i, hits, firstLine(text, 160)}
			}
		case "assistant":
			assistants = append(assistants, scored{i, hits, firstLine(text, 220)})
		}
	}
	sort.SliceStable(assistants, func(i, j int) bool { return assistants[i].hits > assistants[j].hits })
	if len(assistants) > 2 {
		assistants = assistants[:2]
	}
	// Back into the order they were said: two conclusions read as a sequence,
	// and keeping them in score order tells the story backwards.
	sort.SliceStable(assistants, func(i, j int) bool { return assistants[i].idx < assistants[j].idx })
	out := make([]string, 0, len(assistants))
	for _, a := range assistants {
		out = append(out, a.text)
	}
	return bestUser.text, out
}

// densestLine returns the line of a message carrying the most query terms, and
// how many, so a message is both chosen and quoted where it answers rather
// than where it opens.
func densestLine(text string, terms []string) (string, int) {
	best, bestHits := "", 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if h := termHits(line, terms); h > bestHits {
			best, bestHits = line, h
		}
	}
	if best == "" {
		return text, 0
	}
	return best, bestHits
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
		low := strings.ToLower(c)
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
		line, hits := densestLine(m.Text, terms)
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

func noiseMessage(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range []string{"<local-command", "<command-", "<task-notification", "<teammate-message", "<bash-", "Caveat:", "<system-reminder"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// digestLine is one row of the digest this file injects into an agent's
// context. The rows are built here rather than by the printer, so they never
// went through SafeText: a zero-width space in a recalled reply reached the
// hook context intact, and a project name is markdown a session never wrote.
// Confining each field to one line stops it forging a row of its own.
func digestLine(s string) string { return SafeLine(s) }

func firstLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
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
	low := strings.ToLower(strings.TrimSpace(problem))
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
			for _, w := range strings.Fields(strings.ToLower(m.Text)) {
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
			if snippetOverlap(words[i], words[j]) < 0.6 {
				continue
			}
			out[a.ID] = b.Updated.Format("2006-01-02")
		}
	}
	return out
}
