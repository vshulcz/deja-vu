package search

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
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
	return AutoRecallDigestForAsked(ss, budget, terms, "")
}

// AutoRecallDigestForAsked is AutoRecallDigestFor knowing what was just typed,
// so the block does not open by handing it back. A person repeats an
// instruction — "да, начинай с ретрая" — and the session holding the earlier
// copy wins the opening slot on carrying every word of it, being the same
// sentence. Measured on a real store, that was 22 of 104 injected blocks.
func AutoRecallDigestForAsked(ss []model.Session, budget int, terms []string, asked string) string {
	if budget <= 0 {
		budget = 2000
	}
	var b strings.Builder
	for _, s := range ss {
		if b.Len() >= budget {
			break
		}
		section := autoRecallSessionForAsked(s, time.Now(), false, terms, asked)
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
	// Chinese, Japanese and Korean put no separator between words, so the split
	// above returns whole sentences: a session written in them counted two or
	// three "words" and fell under the bar auto-recall sets for having anything
	// to say (#1342). The index already reads those scripts as overlapping
	// bigrams; this counts them the same way.
	for _, b := range cjkfold.Bigrams(s) {
		set[b] = true
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
// which lines of a matched session are worth showing.
//
// Case folding alone was not enough. The ranking reaches a session through a
// stem fold, and this did not: asked "где именно теперь происходит индексация",
// the ranking found the session that says "индексацию перенесли в фоновый
// воркер" and this found no line in it, so the block fell back to the top of
// the transcript and showed "погоди, а что там дальше по задаче". The comment
// here used to say that costs the line its place and never costs correctness —
// it costs the reader the whole block.
//
// So a long term also matches on its stem: inflection changes the ending, not
// the first several characters. Short terms keep the exact rule, where a
// trimmed prefix would match far too much.
func termHits(text string, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	return TermHitsLowered(strings.ToLower(text), terms)
}

// TermHitsLowered is termHits over text a caller has already lowercased. The
// terms are expected lowercase too, which is what the extractor produces —
// prompt.Terms lowercases the whole prompt before splitting it.
// The
// per-prompt hook asks the same question of every message of a candidate twice
// — once to narrow the session, once to rank what is left — and a marathon
// session is thousands of messages, so lowercasing them once rather than twice
// is worth the second entry point. Measured on a real store, narrowing a
// candidate was 147 ms of the 284 an answering call takes.
func TermHitsLowered(low string, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	n := 0
	for _, t := range terms {
		if t == "" {
			continue
		}
		if containsWord(low, t) {
			n++
			continue
		}
		// The stem is checked the same way: it may run on to the right, that
		// being the ending it exists for, but it still has to start a word.
		// Without that "индексация" matched inside "переиндексацию".
		if stem := termStem(t); stem != "" && containsWord(low, stem) {
			n++
		}
	}
	return n
}

// wholeWordMax is how short a term has to be before it must be a whole word.
//
// Traced by instrumenting the matcher on a real store: "mini" scored a hit on
// "cron minimum granularity is 1 minute", and that row of a table then won the
// slot the reader sees first. A word of four letters or fewer sits inside too
// many others to be trusted as a fragment.
//
// Deliberately low. Requiring it of everything below seven characters was
// measured too: it cost the benchmark 14/14 -> 13/14 and the live rate 88% ->
// 86%, because "hermes", "score" and "tick" legitimately appear inside longer
// words the reader wants.
const wholeWordMax = 4

// containsWord finds a term where a word starts. A short one must end there
// too; a longer one may run on, which is what an ending or a compound looks
// like — "hermes/config", "индексацию".
func containsWord(text, term string) bool {
	if term == "" {
		return false
	}
	whole := len([]rune(term)) <= wholeWordMax
	for at := 0; ; {
		i := strings.Index(text[at:], term)
		if i < 0 {
			return false
		}
		i += at
		startsWord := !wordRuneBefore(text, i)
		endsWord := !whole || !wordRuneAt(text, i+len(term))
		if startsWord && endsWord {
			return true
		}
		at = i + 1
		if at >= len(text) {
			return false
		}
	}
}

// wordRuneBefore and wordRuneAt decode whole runes: indexing a byte lands in
// the middle of a Cyrillic letter and reads it as punctuation.
func wordRuneBefore(text string, i int) bool {
	if i <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return isWordRune(r)
}

func wordRuneAt(text string, i int) bool {
	if i >= len(text) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text[i:])
	return isWordRune(r)
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') || r >= 0x400
}

// termStemMin is how long a term must be before its ending may be trimmed.
// Deliberately not a stemmer: deja indexes a dozen languages and a real one for
// each is a different project. Dropping two characters covers the case endings
// that made the difference here without letting "index" match "indeed".
const termStemMin = 7

// TextCarriesTerm reports whether a line holds a term, folding case and, for a
// long term, its inflected endings. Exported so a caller scoring what the
// block shows applies the same rule the block was built with — the two drifted
// once already, in the gate, and the benchmark then measured a bar the product
// did not have.
func TextCarriesTerm(text, term string) bool {
	return termHits(text, []string{term}) > 0
}

// TextCarriesAnyTerm is TextCarriesTerm over a whole query. Asking term by term
// lowercases the message once per term, and the per-prompt hook does that for
// every message of a session that can run to sixteen thousand of them: measured
// on a real store, narrowing the candidates cost 498 ms of the hook's 577.
func TextCarriesAnyTerm(text string, terms []string) bool {
	return termHits(text, terms) > 0
}

// termStem is a term with its last two characters dropped, or empty when the
// term is too short for that to still identify it.
func termStem(t string) string {
	r := []rune(strings.ToLower(t))
	if len(r) < termStemMin {
		return ""
	}
	return string(r[:len(r)-2])
}

func autoRecallSessionFor(s model.Session, now time.Time, provenance bool, terms []string) string {
	return autoRecallSessionForAsked(s, now, provenance, terms, "")
}

func autoRecallSessionForAsked(s model.Session, now time.Time, provenance bool, terms []string, asked string) string {
	// A harness check has nothing to recall: it exists to prove the wiring
	// works, and quoting it hands the reader a test fixture.
	if isHarnessCheck(s) {
		return ""
	}
	var problem string
	var conclusions []string
	matched := false
	if len(terms) > 0 {
		problem, conclusions = matchedLinesAsked(s, terms, asked)
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
		//
		// The same holds for the question line, and it was not applied there.
		// When nothing the user said matched, this loop took the first thing
		// they said in the whole session — on a long one that is "carry on
		// then". Measured on a real store: a recall that had found the right
		// session opened with "продолжай дальше" and read as worthless, which
		// is how a correct answer teaches an agent to stop reading these.
		// Better to show the matched line alone than to frame it with filler.
		// With terms in hand this skips the question line too; without them the
		// block still opens with the session's own first question, which is
		// what the session-start digest is for.
		if matched && (m.Role == "assistant" || len(terms) > 0) {
			continue
		}
		text := contextText(m.Text, false)
		if strings.TrimSpace(text) == "" || saysItHasNoMemory(text) {
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
			// The reader's zone, like the provenance line above and the ctx
			// header: this digest is injected on every prompt, so it is the
			// date an agent is most likely to repeat back, and it must not
			// name a different day from the one on the user's screen (#856).
			date = " · " + s.Updated.Local().Format("2006-01-02")
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
	return matchedLinesAsked(s, terms, "")
}

func matchedLinesAsked(s model.Session, terms []string, asked string) (string, []string) {
	type scored struct {
		idx, hits int
		text      string
	}
	var bestUser scored
	var assistants []scored
	// Which messages carried a query word, so the lines just after them can be
	// considered for the conclusion slot below.
	matchedAt := make(map[int]bool, 8)
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
		matchedAt[i] = true
		// Among lines that carry the question words, prefer the one that
		// concluded something. Five ways of choosing by where and how often a
		// word fell were measured and none moved the number; these markers are
		// what the session-start digest already uses to tell a decision from a
		// passing mention.
		// Among lines that carry the question words, prefer the one that
		// concluded something.
		if digest.CarriesDecision(line) {
			hits++
		}
		// Among lines that carry the question's words, prefer the one that
		// concluded something. Five ways of choosing by where and how often a
		// word fell were measured and none moved the number: "I'll look at the
		// shard later" and "we moved the shard to the region" are the same line
		// to all of them. These markers are what the session-start digest
		// already uses to tell one from the other.
		// Terms arrive most-identifying first when the caller knows the order,
		// so a line carrying the word that identified the match beats one
		// carrying an ordinary word the same session happens to use. Without
		// this every query word weighs the same and "decide" wins on being
		// said three times.
		// Judged on the excerpt, not the line: the excerpt is what the reader
		// sees, and it can carry the admission in from a neighbouring line.
		if saysItHasNoMemory(contextText(line, false)) {
			continue
		}
		hits = hits*len(terms) + rankOfBestTerm(line, terms)
		text := contextText(line, false)
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch m.Role {
		case "user":
			if !noiseMessage(m.Text) && !nearCopy(line, asked) && hits > bestUser.hits {
				bestUser = scored{i, hits, lineAround(text, 160, terms)}
			}
		case "assistant":
			assistants = append(assistants, scored{i, hits, lineAround(text, 220, terms)})
		}
	}
	// Among the agent's lines, prefer the ones that settled something. Within a
	// message the densest line already yields to a concluding one (#1488), but
	// which messages get the two slots was still decided by word count alone,
	// so a session's conclusion lost its place to a line that merely repeated
	// the question's words. Measured live: of the blocks carrying no
	// conclusion, a third had one in the very session they came from.
	// The preference only counts for a line that also names what was asked
	// about: terms arrive most-identifying first, so that is the first one. A
	// concluding line that does not say the subject settles some other
	// question, and putting it first reads as an answer to this one — measured
	// live, preferring conclusions without this cost one answer of 58.
	// A conclusion often does not repeat the question's words — "in the end we
	// settled on 40" follows the line that named the pool — so it never becomes
	// a candidate at all. Take the agent's next lines after a matched one as
	// candidates for the second slot when they conclude something: measured on
	// a real store, of the blocks carrying no conclusion, a third had one in
	// the session they came from, and this is why.
	if len(matchedAt) > 0 {
		for i, m := range s.Messages {
			if m.Role != "assistant" || matchedAt[i] {
				continue
			}
			if !nearMatch(matchedAt, i, replyWindow) {
				continue
			}
			// Checked here as well as where the slot is filled: this keeps
			// the candidate list short rather than deciding anything, so
			// removing it changes cost and not output.
			line, _ := densestLine(m.Text, terms)
			if line == "" || !digest.CarriesDecision(line) || saysItHasNoMemory(line) {
				continue
			}
			if text := contextText(line, false); strings.TrimSpace(text) != "" {
				assistants = append(assistants, scored{i, 0, lineAround(text, 220, terms)})
			}
		}
	}
	// Two slots, and they answer different questions: one says what the session
	// was about, the other what it settled. Filling both by word count gave two
	// lines of the first kind — measured on a real store, 35 blocks of 100
	// carried a conclusion while a third of the rest had one in the same
	// session. Filling both by conclusion gave blocks that concluded something
	// about a neighbouring subject. One of each is what a reader needs, and it
	// is the packaging that decides how much the retrieval is worth (surveys of
	// agent memory put packaging above ranking for exactly this reason).
	sort.SliceStable(assistants, func(i, j int) bool { return assistants[i].hits > assistants[j].hits })
	if len(assistants) > 2 {
		best := assistants[0]
		rest := assistants[1:]
		pick := -1
		for i, a := range rest {
			if digest.CarriesDecision(a.text) {
				pick = i
				break
			}
		}
		if pick >= 0 {
			assistants = []scored{best, rest[pick]}
		}
	}
	// The block opens with what the user said, and that slot is filled by the
	// best-matching user line whatever it carries. When the session says an
	// ordinary word of the question in one place and answers it in another,
	// that puts the ordinary line first: measured live, "what did we decide
	// about mm_status" opened on "1. **Decide**: User settings or project
	// settings?" while the answer sat two lines below. If a line the agent
	// wrote is worth more, lead with that instead of framing it with this.
	if len(assistants) > 0 && assistants[0].hits > bestUser.hits {
		bestUser = scored{}
	}
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

// replyWindow is how far after a matched line the reply to it may sit. A
// harness writes tool calls and status lines in between, so the conclusion is
// rarely the very next message and never far.
const replyWindow = 4

// nearMatch says whether index i follows a matched message within the window.
func nearMatch(matched map[int]bool, i, window int) bool {
	for j := i - window; j < i; j++ {
		if matched[j] {
			return true
		}
	}
	return false
}

// rankOfBestTerm scores which of the query's terms a line carries, counting the
// earliest in the slice — the most identifying one — for the most.
func rankOfBestTerm(line string, terms []string) int {
	for i, t := range terms {
		if t != "" && TextCarriesTerm(line, t) {
			return len(terms) - i
		}
	}
	return 0
}

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
	for _, line := range strings.Split(text, "\n") {
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
			best, bestHits, bestDecides = line, h, decides
		}
	}
	if best == "" {
		return text, 0
	}
	return best, bestHits
}

// emptyMemoryPhrases are what an agent says when it had no history to work
// from, or when it was refused the tool that would have found some. Quoting
// such a line hands the reader a past failure of this very tool as if it were
// the answer — measured on a real store, 3 blocks of 119 opened on one, two of
// them for the same question about a CAN bus.
var emptyMemoryPhrases = []string{
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
	"ответь ровно",
	"harness live test",
	"smoke test alpha",
}

// saysItHasNoMemory reports whether a line is one of those admissions.
func saysItHasNoMemory(line string) bool {
	low := strings.ToLower(line)
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
		low := strings.ToLower(m.Text)
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
	low := strings.ToLower(s)
	for _, t := range terms {
		if t == "" {
			continue
		}
		i := strings.Index(low, strings.ToLower(t))
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
	start := at - lead
	if start < 0 {
		start = 0
	}
	end := start + n
	if end > len(r) {
		end = len(r)
	}
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
			(r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r >= 0x400
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !wordy(r) }) {
		if len([]rune(w)) >= 3 {
			out[w] = true
		}
	}
	return out
}
