package search

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/query"
)

// The auto-recall modes.
const (
	RecallOff        = "off"
	RecallSafe       = "safe"
	RecallAggressive = "aggressive"
)

// AutoRecallOptions picks and orders the sessions an auto-recall block draws on.
type AutoRecallOptions struct {
	Mode         string
	ProjectNames []string
	Now          time.Time
	// TaskScores ranks sessions by overlap with what the repo is touching
	// right now (harness:ID → matched-file count). Sessions the task points
	// at outrank plain recency; zero or a nil map falls back to recency.
	TaskScores map[string]int
	// Unseen are the sessions this project has not been served recently, by
	// id. They win ties over recency, which is what stops a session start
	// repeating itself: the caller's novelty ordering used to be undone here,
	// because the sort below re-ordered by recency whatever came in, and three
	// separate sessions in one project were handed the same three sessions.
	// Nil leaves the order to task score and recency alone.
	Unseen map[string]bool
}

// AutoRecallResult is a rendered auto-recall block and what went into it.
type AutoRecallResult struct {
	Text     string
	Sessions int
	// RawBytes is the transcript volume of the sessions that made it into
	// Text — the denominator of the distillation ratio deja prints as its
	// own measurement. Candidates the digest skipped never reached an agent,
	// so counting them inflates the ratio.
	RawBytes int64
	// IDs are the sessions that made it into Text, in the order they appear.
	// The session-start hook is the surface that logs them, and without them
	// its repetition could not be counted at all — 606 injections in six weeks
	// with no record of what was in them (#2038).
	IDs []string
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
// sentence. In real-world stores, that was 22 of 104 injected blocks.
func AutoRecallDigestForAsked(ss []model.Session, budget int, terms []string, asked string) string {
	text, _ := AutoRecallDigestShowing(ss, budget, terms, asked)
	return text
}

// AutoRecallDigestShowing is AutoRecallDigestForAsked that also says which
// sessions made it into the text, in the order they appear. The first of them
// is the one to cite: the top-ranked session is skipped when it has nothing
// quotable, and a citation built from the ranking then named a session the
// digest never showed.
func AutoRecallDigestShowing(ss []model.Session, budget int, terms []string, asked string) (string, []model.Session) {
	if budget <= 0 {
		budget = 2000
	}
	// Sections are gathered until non-receipt lines fill the budget, while
	// still looking ahead to at least the top 3 sessions on real budgets so
	// one long session cannot starve the remaining candidates out of secs.
	type section struct {
		s     model.Session
		lines []string
	}
	var secs []section
	core, total := 0, 0
	newestByProject := make(map[string]string)
	for _, s := range ss {
		if core >= budget && (budget < 600 || len(secs) >= min(len(ss), 3)) {
			break
		}
		text := autoRecallSessionForAsked(s, time.Now(), false, terms, asked)
		if text == "" {
			continue
		}
		lines := strings.SplitAfter(text, "\n")
		if len(lines) > 0 {
			if day := extractHeaderDay(lines[0]); day != "" {
				if newest, ok := newestByProject[s.Project]; ok && day < newest {
					lines[0] = annotateEarlierHeader(lines[0], newest)
				} else if !ok || day > newest {
					newestByProject[s.Project] = day
				}
			}
		}
		for _, l := range lines {
			total += len(l)
			if !receiptLine(l) {
				core += len(l)
			}
		}
		secs = append(secs, section{s, lines})
	}
	all := make([][]string, len(secs))
	for i := range secs {
		all[i] = secs[i].lines
	}
	dropReceipts(all, total, budget)
	texts := make([]string, len(secs))
	totalAfterDrop := 0
	for i, sec := range secs {
		texts[i] = strings.Join(sec.lines, "")
		totalAfterDrop += len(texts[i])
	}
	var b strings.Builder
	var shown []model.Session
	shareBudget := len(secs) >= 2 && budget >= 700 && totalAfterDrop > budget
	for i, sec := range secs {
		rem := budget - b.Len()
		if rem <= 0 {
			break
		}
		text := texts[i]
		if len(shown) > 0 && shareBudget {
			if firstNL := strings.IndexByte(text, '\n'); firstNL >= 0 && firstNL+1 < len(text) && rem < len(text) && rem < firstNL+24 {
				break
			}
		}
		limit := rem
		if shareBudget {
			reserve := 0
			switch {
			case i == 0 && len(secs) == 2:
				reserve = min(len(texts[1]), budget/4)
			case i == 0 && len(secs) >= 3 && budget >= 800:
				reserve = min(len(texts[1]), budget/4) + min(len(texts[2]), budget/5)
			case i == 0 && len(secs) >= 3:
				reserve = min(len(texts[1]), budget/4)
			case i == 1 && len(secs) >= 3 && budget >= 800:
				reserve = min(len(texts[2]), budget/5)
			}
			if reserve > 0 && rem-reserve > 0 {
				limit = min(limit, rem-reserve)
			}
		}
		cutText := cutSectionPreservingConclusion(sec.lines, text, limit)
		if cutText == "" {
			break
		}
		shown = append(shown, sec.s)
		b.WriteString(cutText)
	}
	if len(shown) == 1 && shareBudget && b.Len() < len(texts[0]) && b.Len() < budget {
		if full := cutSectionPreservingConclusion(secs[0].lines, texts[0], budget); len(full) > b.Len() {
			b.Reset()
			b.WriteString(full)
		}
	}
	return strings.TrimSpace(b.String()), shown
}

func isConclusionLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	return strings.HasPrefix(trimmed, evidence.LabelModelSaid) ||
		strings.HasPrefix(trimmed, evidence.LabelModel) ||
		strings.HasPrefix(trimmed, "Assistant: ")
}

func sectionHasConclusionLine(s string) bool {
	for line := range strings.SplitSeq(s, "\n") {
		if isConclusionLine(line) {
			return true
		}
	}
	return false
}

func sectionLastConclusionLine(s string) string {
	last := ""
	for line := range strings.SplitSeq(s, "\n") {
		if isConclusionLine(line) {
			last = strings.TrimSpace(line)
		}
	}
	return last
}

func sectionPreservesLastConclusion(full, cut string) bool {
	want := sectionLastConclusionLine(full)
	if want == "" {
		return true
	}
	got := sectionLastConclusionLine(cut)
	if got == "" {
		return false
	}
	if got == want {
		return true
	}
	prefix := strings.TrimSuffix(got, "…")
	if !strings.HasPrefix(want, prefix) {
		return false
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(prefix, "- "))
	for _, lbl := range []string{evidence.LabelModelSaid, evidence.LabelModel, "Assistant: "} {
		if after, ok := strings.CutPrefix(trimmed, lbl); ok {
			return len(strings.TrimSpace(after)) >= 24
		}
	}
	return len(prefix) >= 32
}

func clipLineToBytes(line string, maxBytes int) string {
	hasNL := strings.HasSuffix(line, "\n")
	body := strings.TrimSuffix(line, "\n")
	if len(body) <= maxBytes {
		return line
	}
	cut := max(0, maxBytes-len("…"))
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	if cut == 0 {
		return ""
	}
	out := strings.TrimRight(body[:cut], " \t") + "…"
	if hasNL {
		out += "\n"
	}
	return out
}

func isCompactableUserLine(l string) bool {
	if !strings.HasPrefix(l, "  - ") {
		return false
	}
	trimmed := strings.TrimPrefix(l, "  - ")
	for _, p := range personLabels {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

func compactSectionLines(lines []string, skipIdx map[int]bool, keepClippedUser bool) string {
	out := make([]string, 0, len(lines))
	keptUser := false
	for idx, l := range lines {
		if skipIdx[idx] {
			continue
		}
		if isCompactableUserLine(l) {
			if !keepClippedUser || keptUser {
				continue
			}
			keptUser = true
			if clipped := clipLineToBytes(l, 150); clipped != "" {
				out = append(out, clipped)
			}
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "")
}

func cutSectionPreservingConclusion(lines []string, text string, limit int) string {
	cutText := cutDigestSection(text, limit)
	if sectionPreservesLastConclusion(text, cutText) {
		return cutText
	}
	if cand := cutDigestSection(compactSectionLines(lines, nil, true), limit); cand != "" && sectionPreservesLastConclusion(text, cand) {
		return cand
	}
	withoutUsersCut := cutDigestSection(compactSectionLines(lines, nil, false), limit)
	if withoutUsersCut != "" && sectionPreservesLastConclusion(text, withoutUsersCut) {
		return withoutUsersCut
	}
	var conclIdxs []int
	for idx, l := range lines {
		if isConclusionLine(l) {
			conclIdxs = append(conclIdxs, idx)
		}
	}
	if len(conclIdxs) >= 2 {
		drop := make(map[int]bool, len(conclIdxs)-1)
		tryDrop := func(keepUser bool, allowClipped bool) string {
			cand := cutDigestSection(compactSectionLines(lines, drop, keepUser), limit)
			if cand == "" {
				return ""
			}
			if sectionPreservesLastConclusion(text, cand) || (allowClipped && sectionHasConclusionLine(cand)) {
				return cand
			}
			return ""
		}
		// Drop intermediate conclusion lines first so the opening and final
		// conclusion lines can remain paired whenever both fit within limit.
		for k := len(conclIdxs) - 2; k >= 1; k-- {
			drop[conclIdxs[k]] = true
			if cand := tryDrop(true, false); cand != "" {
				return cand
			}
			if cand := tryDrop(false, false); cand != "" {
				return cand
			}
		}
		drop[conclIdxs[0]] = true
		if cand := tryDrop(true, false); cand != "" {
			return cand
		}
		if cand := tryDrop(false, true); cand != "" {
			return cand
		}
	}
	if !sectionHasConclusionLine(cutText) && withoutUsersCut != "" && sectionHasConclusionLine(withoutUsersCut) {
		return withoutUsersCut
	}
	return cutText
}

func cutDigestSection(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	if nl := strings.LastIndexByte(text[:limit], '\n'); nl > 0 {
		prefix := text[:nl+1]
		if strings.Count(prefix, "\n") >= 2 || nl+1 == len(text) {
			return prefix
		}
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// dropReceipts blanks receipt lines of secs, whose lines come to total bytes,
// until they fit budget. A receipt backs the line that answers; it is not that
// line. They go last shown first: on a real replay the Ran and Output lines in
// front of a conclusion took the room of the conclusion.
func dropReceipts(secs [][]string, total, budget int) {
	for i := len(secs) - 1; i >= 0 && total > budget; i-- {
		for j := len(secs[i]) - 1; j >= 0 && total > budget; j-- {
			if receiptLine(secs[i][j]) {
				total -= len(secs[i][j])
				secs[i][j] = ""
			}
		}
	}
}

// receiptLine reports a block line that is a record backing another line:
// a command run, an output, an edit (evidence.Receipts).
func receiptLine(l string) bool {
	body := strings.TrimPrefix(strings.TrimSpace(l), "- ")
	for _, p := range []string{"Ran: `", "Output: ", evidence.LabelOutputOrder, "Edit "} {
		if strings.HasPrefix(body, p) {
			return true
		}
	}
	return false
}

// settledProbeBudget is how much of a session the ordering reads to ask whether
// it concluded anything. The same budget the block itself renders with, so the
// question is about the lines that would be shown.
const settledProbeBudget = 400

// BuildAutoRecall applies the session-start recall policy while constructing
// the digest. Unknown modes use the safe policy.
func BuildAutoRecall(ss []model.Session, o AutoRecallOptions) AutoRecallResult {
	mode := lower(strings.TrimSpace(o.Mode))
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
	// A session that settled something beats one that did not, whatever their
	// dates. Measured on the context benchmark: the block served six sessions
	// newest-first, four of them carrying "routine update, nothing settled
	// here", and the session holding how the error was fixed never got a slot —
	// a third of the corpus's facts, present in the per-hit digest and missing
	// from the block an agent is handed at session start. The per-prompt hook
	// has led with the concluding session since #1475; this is the same rule on
	// the other surface.
	settled := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		lines := digest.SubstantialConclusions(c, settledProbeBudget, 1)
		settled[c.Key()] = len(lines) > 0 && (digest.CarriesDecision(lines[0]) || digest.HasTrajectoryConclusion(c))
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ti := o.TaskScores[candidates[i].Key()]
		tj := o.TaskScores[candidates[j].Key()]
		if ti != tj {
			return ti > tj
		}
		if len(o.Unseen) > 0 {
			iNew := o.Unseen[candidates[i].Key()] || o.Unseen[candidates[i].ID]
			jNew := o.Unseen[candidates[j].Key()] || o.Unseen[candidates[j].ID]
			if iNew != jNew {
				return iNew
			}
		}
		iSet := settled[candidates[i].Key()]
		jSet := settled[candidates[j].Key()]
		if iSet != jSet {
			return iSet
		}
		iRecent := !candidates[i].Updated.Before(o.Now.AddDate(0, 0, -90))
		jRecent := !candidates[j].Updated.Before(o.Now.AddDate(0, 0, -90))
		if iRecent != jRecent {
			return iRecent
		}
		return candidates[i].Updated.After(candidates[j].Updated)
	})

	budget := 4096
	maxSessions := 7
	if mode == RecallSafe {
		budget = 2048
		maxSessions = 3
	}
	conclusionLimit := 2
	conclusionBudget := 400
	switch len(candidates) {
	case 1:
		conclusionLimit = 5
		conclusionBudget = 1600
		if mode != RecallSafe {
			conclusionLimit = 6
			conclusionBudget = 2400
		}
	case 2:
		conclusionLimit = 3
		conclusionBudget = 1000
		if mode != RecallSafe {
			conclusionLimit = 5
			conclusionBudget = 1800
		}
	}
	var b strings.Builder
	var fingerprints []map[string]bool
	var ids []string
	var raw int64
	for _, s := range candidates {
		if mode == RecallSafe && !projectMatches(s.Project, o.ProjectNames) {
			continue
		}
		sessLimit, sessBudget := conclusionLimit, conclusionBudget
		if mode != RecallSafe && len(candidates) > 1 && (len(s.Messages) >= 40 || s.Held >= 40) && budget-b.Len() >= 1200 {
			sessLimit = max(sessLimit, 5)
			sessBudget = max(sessBudget, 1400)
		}
		section := autoRecallSessionWithLimit(s, o.Now, true, sessBudget, sessLimit)
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
			if len(fingerprints) > 0 {
				if nl := strings.LastIndexByte(section[:cut], '\n'); nl > 0 {
					section = section[:nl+1]
				} else {
					section = ""
				}
				if strings.Count(section, "\n") < 3 {
					section = ""
				}
			} else {
				section = section[:cut]
			}
		}
		if section == "" {
			break
		}
		b.WriteString(section)
		fingerprints = append(fingerprints, fingerprint)
		ids = append(ids, s.ID)
		for _, m := range s.Messages {
			raw += int64(len(m.Text))
		}
		if b.Len() >= budget || len(fingerprints) >= maxSessions {
			break
		}
	}
	return AutoRecallResult{Text: strings.TrimSpace(b.String()), Sessions: len(fingerprints), RawBytes: raw, IDs: ids}
}

func projectMatches(project string, names []string) bool {
	project = filepathClean(project)
	// Delegate to the canonical domain scope rule (query.ProjectInScope) so
	// session-start auto-recall honors both the imported: peer prefix (#E-new33)
	// and the #2333 bare-basename rule (preventing a local "acme/api" session
	// from matching a bare "api" candidate when standing in /work/api).
	for _, name := range names {
		if query.ProjectInScope(project, filepathClean(name)) {
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
	var textSb247 strings.Builder
	for _, m := range s.Messages {
		textSb247.WriteString(" " + m.Text)
	}
	text += textSb247.String()
	return wordSet(text)
}

func wordSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, word := range strings.FieldsFunc(lower(s), func(r rune) bool {
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

func autoRecallSessionWithLimit(s model.Session, now time.Time, provenance bool, conclusionBudget, conclusionLimit int) string {
	return autoRecallSessionForAskedWithLimit(s, now, provenance, nil, "", conclusionBudget, conclusionLimit)
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
	return TermHitsLowered(lower(text), terms)
}

// TermHitsLowered is termHits over text a caller has already lowercased. The
// terms are expected lowercase too, which is what the extractor produces —
// prompt.Terms lowercases the whole prompt before splitting it.
// The
// per-prompt hook asks the same question of every message of a candidate twice
// — once to narrow the session, once to rank what is left — and a marathon
// session is thousands of messages, so lowercasing them once rather than twice
// is worth the second entry point. In real-world stores, narrowing a
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
	eInflect := whole && len(term) == 4 && term[3] == 'e'
	if containsWordMatch(text, term, whole, eInflect, eInflect) {
		return true
	}
	if root, ok := regularSingular(term); ok {
		return containsWordMatch(text, root, true, true, false)
	}
	return false
}

func containsWordMatch(text, term string, whole, allowS, allowD bool) bool {
	for at := 0; ; {
		i := strings.Index(text[at:], term)
		if i < 0 {
			return false
		}
		i += at
		startsWord := !wordRuneBefore(text, i)
		after := i + len(term)
		endsWord := !whole || !wordRuneAt(text, after) ||
			(after < len(text) && ((allowS && text[after] == 's') || (allowD && text[after] == 'd')) && !wordRuneAt(text, after+1))
		if startsWord && endsWord {
			return true
		}
		at = i + 1
		if at >= len(text) {
			return false
		}
	}
}

// regularSingular returns the 4-5 letter root of a regular ASCII -s plural
// (such as "bikes" -> "bike" or "weeks" -> "week") so short singular/plural
// forms match below termStemMin without widening unrelated substring hits.
func regularSingular(term string) (string, bool) {
	n := len(term)
	if n != 5 && n != 6 {
		return "", false
	}
	if term[n-1] != 's' {
		return "", false
	}
	prev := term[n-2]
	if prev == 's' || prev == 'u' || prev == 'i' {
		return "", false
	}
	for i := range n {
		if term[i] < 'a' || term[i] > 'z' {
			return "", false
		}
	}
	return term[:n-1], true
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
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
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
	r := []rune(lower(t))
	if len(r) < termStemMin {
		return ""
	}
	return string(r[:len(r)-2])
}

func autoRecallSessionForAsked(s model.Session, now time.Time, provenance bool, terms []string, asked string) string {
	return autoRecallSessionForAskedWithLimit(s, now, provenance, terms, asked, 400, 2)
}

func autoRecallSessionForAskedWithLimit(s model.Session, now time.Time, provenance bool, terms []string, asked string, conclusionBudget, conclusionLimit int) string {
	// A harness check has nothing to recall: it exists to prove the wiring
	// works, and quoting it hands the reader a test fixture.
	if isHarnessCheck(s) {
		return ""
	}
	if conclusionBudget <= 0 {
		conclusionBudget = 400
	}
	if conclusionLimit <= 0 {
		conclusionLimit = 2
	}
	var problem string
	var problems []string
	var conclusions []string
	var matched bool
	var matchedAt map[int]bool
	if len(terms) > 0 {
		problems, conclusions, matchedAt = matchedLinesWithMatchesAskedUsers(s, terms, asked)
		problem = strings.Join(problems, "\n")
		matched = len(conclusions) > 0
	} else {
		// No question yet — this is the block handed over at session start.
		// Nothing can be matched, and walking the transcript from the top takes
		// the first two things the agent said, which are "let me look" and "I
		// have found the file". What the session decided is at the end of it.
		//
		// digest.SubstantialConclusions picks the decision-carrying lines, newest
		// first, and is what `deja share` prints under the same heading.
		conclusions = digest.SubstantialConclusions(s, conclusionBudget, conclusionLimit)
		matched = len(conclusions) > 0
		if matched {
			problem = sessionStartProblemForConclusions(s, conclusions)
		}
	}
	fallbackStart := -1
	fallbackEnd := len(s.Messages)
	if len(terms) > 0 && !matched && problem != "" {
		fallbackStart = earliestMatchIndex(len(s.Messages), matchedAt)
		if fallbackStart >= 0 {
			fallbackEnd = nextStandaloneUserTurn(s.Messages, matchedAt, fallbackStart)
		}
	}
	for i, m := range s.Messages {
		if i < fallbackStart {
			continue
		}
		if fallbackStart >= 0 && i >= fallbackEnd {
			break
		}
		if problem != "" && (len(conclusions) >= conclusionLimit || matched) {
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
		// then". In real-world stores, a recall that had found the right
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
			// A line that names nothing is not the session's problem. The
			// digest opened on "продолжай" — a real message, not plumbing, and
			// the whole first block then said "go on" and "nothing settled
			// yet" while the session that settled the thing came second
			// (#3412). prompt.Terms is what the recall side already searches
			// on: no terms means there is nothing in the line to search for.
			if problem == "" && !noiseMessage(m.Text) && len(prompt.Terms(text)) > 0 {
				problem = firstLine(text, 160)
			}
		case "assistant":
			// The fallback is held to the same bar as the conclusions it
			// stands in for, or a session that settled nothing still opened
			// on "Hi. What do you want to work on?" or "Waiting for CI.".
			if len(conclusions) < conclusionLimit && digest.SaysSomething(text) {
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
	// A rule the person gave about checking or building this project goes
	// first, under the one mark the frame says to follow, whether or not it
	// matched: on the stand a rule said in a session's second turn never
	// reached the block, which showed the first turn's output.
	if len(problems) == 0 && problem != "" {
		problems = []string{problem}
	}
	userRuleLines(&b, s, "  - ")
	problemLine := func() {
		for _, p := range problems {
			if p == "" || IsUserRule(s, p) {
				continue
			}
			if l, ok := scrubLine(s, personLabel(s, p)+p); ok {
				fmt.Fprintf(&b, "  - %s\n", digestLine(l))
			}
		}
	}
	if s.Path != "" && evidenceReader("").Covers(s.Updated) {
		problemLine()
		for _, c := range evidenceRewrite(s, "", conclusions, true, problem) {
			if IsUserRule(s, strings.TrimPrefix(c, evidence.LabelUserRule)) {
				continue
			}
			fmt.Fprintf(&b, "  - %s\n", digestLine(c))
		}
		return b.String()
	}
	problemLine()
	for _, c := range conclusions {
		if l, ok := scrubLine(s, "Assistant: "+c); ok {
			fmt.Fprintf(&b, "  - %s\n", digestLine(l))
		}
	}
	return b.String()
}

// sessionStartProblemForConclusions picks the user question that initiated the
// episode `conclusions` came from. On a multi-topic marathon session, message 0
// can be thousands of messages and several topics earlier than the concluding
// turn at the tail ("queue consumer stalled" at the start vs a replica lag
// routing diagnosis at message 13,104), splicing two unrelated systems into one
// digest block.
func sessionStartProblemForConclusions(s model.Session, conclusions []string) string {
	if len(conclusions) == 0 {
		return ""
	}
	firstIdx := -1
	firstProblem := ""
	for i, m := range s.Messages {
		if m.Role != "user" || noiseMessage(m.Text) || digest.IsCompactionSummary(strings.TrimSpace(m.Text)) {
			continue
		}
		text := contextText(m.Text, false)
		if strings.TrimSpace(text) == "" || saysItHasNoMemory(text) {
			continue
		}
		if len(prompt.Terms(text)) > 0 {
			firstIdx = i
			firstProblem = firstLine(text, 160)
			break
		}
	}
	if firstIdx < 0 {
		return ""
	}
	probe := strings.TrimSpace(strings.TrimSuffix(conclusions[0], "…"))
	if len(probe) > 48 {
		probe = strings.TrimSpace(digest.UTF8SafeCut(probe, 48))
	}
	concIdx := -1
	for i, m := range slices.Backward(s.Messages) {
		if m.Role != "assistant" {
			continue
		}
		if probe != "" && (strings.Contains(m.Text, probe) || strings.Contains(digest.MessageText(m.Text), probe)) {
			concIdx = i
			break
		}
	}
	if concIdx <= firstIdx+12 {
		return firstProblem
	}
	concText := conclusions[0]
	if TextCarriesAnyTerm(concText, prompt.Terms(firstProblem)) {
		return firstProblem
	}
	nearest := ""
	floor := max(firstIdx+1, concIdx-80)
	for i := concIdx - 1; i >= floor; i-- {
		m := s.Messages[i]
		if m.Role != "user" || noiseMessage(m.Text) || digest.IsCompactionSummary(strings.TrimSpace(m.Text)) || digest.IsTrivialContinuation(m.Text) {
			continue
		}
		text := contextText(m.Text, false)
		if strings.TrimSpace(text) == "" || saysItHasNoMemory(text) {
			continue
		}
		uTerms := prompt.Terms(text)
		if len(uTerms) == 0 {
			continue
		}
		line := firstLine(text, 160)
		if TextCarriesAnyTerm(concText, uTerms) {
			return line
		}
		if nearest == "" && len(uTerms) >= 2 {
			nearest = line
		}
	}
	if nearest != "" {
		return nearest
	}
	return firstProblem
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
	users, out := matchedLinesAskedUsers(s, terms, asked)
	if len(users) == 0 {
		return "", out
	}
	return users[0], out
}

type matchedScoredLine struct {
	idx, hits      int
	text           string
	tailConclusion bool
}

func matchedLinesAskedUsers(s model.Session, terms []string, asked string) ([]string, []string) {
	userTexts, assistantTexts, _ := matchedLinesWithMatchesAskedUsers(s, terms, asked)
	return userTexts, assistantTexts
}

func matchedLinesWithMatchesAskedUsers(s model.Session, terms []string, asked string) ([]string, []string, map[int]bool) {
	trajConcl := digest.TrajectoryConclusionMessages(s.Messages)
	userCands, assistants, matchedAt := collectMatchedSessionLines(s, terms, asked, trajConcl)
	assistants = appendMatchedReplyConclusions(s, terms, trajConcl, matchedAt, userCands, assistants)
	assistants, assistantTexts := selectMatchedAssistantLines(assistants, trajConcl, len(userCands) > 0, terms)
	userTexts := selectMatchedUserLinesInSession(s, userCands, assistants, terms, asked)
	return userTexts, assistantTexts, matchedAt
}

func collectMatchedSessionLines(s model.Session, terms []string, asked string, trajConcl map[int]bool) ([]matchedScoredLine, []matchedScoredLine, map[int]bool) {
	var userCands []matchedScoredLine
	var assistants []matchedScoredLine
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
		msgText := m.Text
		if m.Role == "assistant" {
			msgText = digest.StripAssistantBoilerplate(m.Text)
		}
		line, hits := densestLine(msgText, terms)
		if hits == 0 {
			continue
		}
		// An envelope can carry the query's words and say nothing a reader
		// wanted: a wrapped inter-agent notification quoting a session id
		// matched, and the block opened with truncated JSON (#2735).
		if digest.IsPlumbing(line) || digest.IsToolCallRecord(line) || (m.Role == "assistant" && digest.IsCelebratoryBoilerplate(line)) {
			continue
		}
		matchedAt[i] = true
		// Among lines that carry the question's words, prefer the one that
		// concluded something. Five ways of choosing by where and how often a
		// word fell were measured and none moved the number: "I'll look at the
		// shard later" and "we moved the shard to the region" are the same line
		// to all of them. These markers are what the session-start digest
		// already uses to tell one from the other.
		// One bullet of a document the agent wrote is not what the session
		// concluded, however well it matches (#2740).
		if digest.IsDocumentItem(msgText, line) {
			continue
		}
		if (digest.CarriesDecision(line) || trajConcl[i]) && (hits >= 2 || TextCarriesTerm(line, terms[0])) {
			hits++
		}
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
			if digest.CarriesUserCorrection(line) {
				hits += len(terms)
			}
			if carriesConcreteQuantityOrUpdate(line) {
				hits++
			}
			if !noiseMessage(m.Text) && !nearCopy(line, asked) {
				userCands = append(userCands, matchedScoredLine{idx: i, hits: hits, text: lineAround(text, 160, terms)})
			}
		case "assistant":
			assistants = append(assistants, matchedScoredLine{idx: i, hits: hits, text: lineAround(text, 320, terms)})
		}
	}
	return userCands, assistants, matchedAt
}

func appendMatchedReplyConclusions(s model.Session, terms []string, trajConcl, matchedAt map[int]bool, userCands, assistants []matchedScoredLine) []matchedScoredLine {
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
	if len(matchedAt) == 0 {
		return assistants
	}
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
		line, _ := densestLine(digest.StripAssistantBoilerplate(m.Text), terms)
		if line == "" || (!digest.CarriesDecision(line) && !trajConcl[i]) || saysItHasNoMemory(line) || digest.IsCelebratoryBoilerplate(line) {
			continue
		}
		// The same two bars the loop above applies. This path reached the
		// slot without them, so a captured `⚙ deja_recall {"query":…}`
		// carrying a decision word — and the log of a question carries the
		// question's words by construction — was quoted as the answer to
		// the question it recorded (#2067).
		if digest.IsPlumbing(line) || digest.IsToolCallRecord(line) || (m.Role == "assistant" && digest.IsCelebratoryBoilerplate(line)) {
			continue
		}
		if text := contextText(line, false); strings.TrimSpace(text) != "" {
			assistants = append(assistants, matchedScoredLine{idx: i, text: lineAround(text, 320, terms)})
		}
	}
	return appendSingleTaskTrajectoryConclusion(s, terms, trajConcl, matchedAt, userCands, assistants)
}

func earliestMatchIndex(msgCount int, matchedAt map[int]bool) int {
	for i := range msgCount {
		if matchedAt[i] {
			return i
		}
	}
	return -1
}

func strongestMatchIndex(msgCount int, matchedAt map[int]bool, userCands, assistants []matchedScoredLine) int {
	bestIdx := -1
	bestHits := -1
	for _, a := range assistants {
		if a.hits > bestHits || (a.hits == bestHits && a.idx > bestIdx) {
			bestHits = a.hits
			bestIdx = a.idx
		}
	}
	for _, u := range userCands {
		if u.hits > bestHits || (u.hits == bestHits && u.idx > bestIdx) {
			bestHits = u.hits
			bestIdx = u.idx
		}
	}
	if bestIdx >= 0 {
		return bestIdx
	}
	return earliestMatchIndex(msgCount, matchedAt)
}

func turnStartForMatch(msgs []model.Message, anchorIdx int) int {
	for k := min(anchorIdx, len(msgs)-1); k >= 0; k-- {
		m := msgs[k]
		if m.Role != "user" || noiseMessage(m.Text) || digest.IsPlumbing(m.Text) || digest.IsAgentArtifact(m.Text) {
			continue
		}
		if len(prompt.Terms(contextText(m.Text, false))) >= 2 {
			return k
		}
	}
	return max(0, anchorIdx)
}

func nextStandaloneUserTurn(msgs []model.Message, matchedAt map[int]bool, afterIdx int) int {
	for k := afterIdx + 1; k < len(msgs); k++ {
		m := msgs[k]
		if m.Role != "user" || matchedAt[k] {
			continue
		}
		if noiseMessage(m.Text) || digest.IsPlumbing(m.Text) || digest.IsAgentArtifact(m.Text) {
			continue
		}
		if len(prompt.Terms(contextText(m.Text, false))) >= 2 {
			return k
		}
	}
	return len(msgs)
}

func appendSingleTaskTrajectoryConclusion(s model.Session, terms []string, trajConcl, matchedAt map[int]bool, userCands, assistants []matchedScoredLine) []matchedScoredLine {
	anchorIdx := strongestMatchIndex(len(s.Messages), matchedAt, userCands, assistants)
	if anchorIdx < 0 {
		return assistants
	}
	epStart := turnStartForMatch(s.Messages, anchorIdx)
	taskEnd := nextStandaloneUserTurn(s.Messages, matchedAt, max(anchorIdx, epStart))
	sawAction := false
	lastTrajIdx := -1
	lastAsstIdx := -1
	subMsgs := make([]model.Message, 0, taskEnd-epStart)
	for k := epStart; k < taskEnd; k++ {
		m := s.Messages[k]
		if trajConcl[k] {
			sawAction = true
			lastTrajIdx = k
		} else if m.Role == "edit" || m.Role == "wrote" || (m.Role == "command" && strings.Contains(m.Text, "→ exit 0")) {
			sawAction = true
		}
		if m.Role == "assistant" {
			lastAsstIdx = k
		}
		subMsgs = append(subMsgs, m)
	}
	if !sawAction || lastAsstIdx < 0 {
		return assistants
	}
	concls := digest.SubstantialConclusions(model.Session{ID: s.ID, Project: s.Project, Messages: subMsgs}, 320, 1)
	if len(concls) == 0 {
		return assistants
	}
	line := concls[0]
	if line == "" || digest.IsPlumbing(line) || digest.IsToolCallRecord(line) || digest.IsCelebratoryBoilerplate(line) || saysItHasNoMemory(line) {
		return assistants
	}
	text := contextText(line, false)
	if strings.TrimSpace(text) == "" {
		return assistants
	}
	cand := lineAround(text, 320, terms)
	conclIdx := lastAsstIdx
	if lastTrajIdx >= 0 {
		conclIdx = lastTrajIdx
	}
	if probe := digest.UTF8SafeCut(strings.TrimSpace(line), 48); probe != "" {
		for k := taskEnd - 1; k >= epStart; k-- {
			if s.Messages[k].Role == "assistant" && strings.Contains(s.Messages[k].Text, probe) {
				conclIdx = k
				break
			}
		}
	}
	for k, a := range assistants {
		if a.idx == conclIdx && nearCopy(a.text, cand) {
			assistants[k].text = cand
			assistants[k].tailConclusion = true
			return assistants
		}
	}
	assistants = append(assistants, matchedScoredLine{idx: conclIdx, text: cand, tailConclusion: true})
	return assistants
}

func selectMatchedAssistantLines(assistants []matchedScoredLine, trajConcl map[int]bool, hasUserMatch bool, terms []string) ([]matchedScoredLine, []string) {
	// Two slots, and they answer different questions: one says what the session
	// was about, the other what it settled. Filling both by word count gave two
	// lines of the first kind — In real-world stores, 35 blocks of 100
	// carried a conclusion while a third of the rest had one in the same
	// session. Filling both by conclusion gave blocks that concluded something
	// about a neighbouring subject. One of each is what a reader needs, and it
	// is the packaging that decides how much the retrieval is worth (surveys of
	// agent memory put packaging above ranking for exactly this reason).
	if len(assistants) > 1 {
		filtered := make([]matchedScoredLine, 0, len(assistants))
		for _, a := range assistants {
			if digest.AnnouncesPlan(a.text) && !digest.CarriesDecision(a.text) && !trajConcl[a.idx] && !a.tailConclusion {
				continue
			}
			filtered = append(filtered, a)
		}
		if len(filtered) > 0 {
			assistants = filtered
		}
	}
	sort.SliceStable(assistants, func(i, j int) bool { return assistants[i].hits > assistants[j].hits })
	vIdx := -1
	bestTrajIdx := -1
	for i, a := range assistants {
		if !a.tailConclusion && !trajConcl[a.idx] {
			continue
		}
		if vIdx < 0 || a.idx > bestTrajIdx || (a.idx == bestTrajIdx && a.tailConclusion && !assistants[vIdx].tailConclusion) {
			vIdx = i
			bestTrajIdx = a.idx
		}
	}
	if vIdx >= 0 {
		v := assistants[vIdx]
		subjectCovered := hasUserMatch || termHits(v.text, terms) > 0
		var bestOther matchedScoredLine
		foundOther := false
		for i, a := range assistants {
			if i == vIdx || nearCopy(a.text, v.text) {
				continue
			}
			if subjectCovered && a.idx == v.idx {
				continue
			}
			if subjectCovered && a.idx < v.idx && !a.tailConclusion && !trajConcl[a.idx] {
				continue
			}
			bestOther = a
			foundOther = true
			break
		}
		if foundOther {
			assistants = []matchedScoredLine{bestOther, v}
		} else {
			assistants = []matchedScoredLine{v}
		}
	} else if len(assistants) > 2 {
		best := assistants[0]
		rest := assistants[1:]
		pick := -1
		for i, a := range rest {
			if digest.CarriesDecision(a.text) && !nearCopy(best.text, a.text) {
				pick = i
				break
			}
		}
		if pick >= 0 {
			assistants = []matchedScoredLine{best, rest[pick]}
		}
	}
	// Three quotes, not two. The pair rule above already trims to two whenever
	// a session has a conclusion to pair with its subject, so this only widens
	// short sessions that have neither — and there the third line is what
	// carries the identifying word.
	if len(assistants) > 3 {
		assistants = assistants[:3]
	}
	// Back into the order they were said: two conclusions read as a sequence,
	// and keeping them in score order tells the story backwards.
	sort.SliceStable(assistants, func(i, j int) bool { return assistants[i].idx < assistants[j].idx })
	out := make([]string, 0, len(assistants))
	for _, a := range assistants {
		out = append(out, a.text)
	}
	return assistants, out
}

func selectMatchedUserLines(userCands []matchedScoredLine, terms []string) []string {
	return selectMatchedUserLinesInSession(model.Session{}, userCands, nil, terms, "")
}

func selectMatchedUserLinesInSession(s model.Session, userCands, assistants []matchedScoredLine, terms []string, asked string) []string {
	// The block opens with what the user said, and that slot is filled by the
	// best-matching user line whatever it carries. When the session says an
	// ordinary word of the question in one place and answers it in another,
	// that puts the ordinary line first: measured live, "what did we decide
	// about mm_status" opened on "1. **Decide**: User settings or project
	// settings?" while the answer sat two lines below. If a line the agent
	// wrote is worth more, lead with that instead of framing it with this.
	nTerms := max(1, len(terms))
	sort.SliceStable(userCands, func(i, j int) bool {
		if userCands[i].hits != userCands[j].hits {
			return userCands[i].hits > userCands[j].hits
		}
		return userCands[i].idx > userCands[j].idx
	})
	var eligible []matchedScoredLine
	for _, u := range userCands {
		if u.text == "" {
			continue
		}
		if len(assistants) > 0 && u.hits < 2*nTerms && assistants[0].hits >= u.hits+2*nTerms && !digest.CarriesUserCorrection(u.text) {
			continue
		}
		if len(assistants) > 0 && !assistants[0].tailConclusion && !digest.CarriesUserCorrection(u.text) {
			dist := u.idx - assistants[0].idx
			if dist < 0 {
				dist = -dist
			}
			if dist > 30 && len(terms) > 0 && (!TextCarriesTerm(u.text, terms[0]) || !TextCarriesTerm(assistants[0].text, terms[0])) {
				continue
			}
		}
		eligible = append(eligible, u)
	}
	var keptUsers []matchedScoredLine
	if len(eligible) > 0 {
		keptUsers = append(keptUsers, eligible[0])
		if second, ok := selectDiverseSecondUserLine(s, eligible[0], eligible[1:], terms); ok {
			keptUsers = append(keptUsers, second)
		} else if adj, ok := adjacentProperNounUserLine(s, eligible[0], asked); ok {
			keptUsers = append(keptUsers, adj)
		}
	}
	sort.SliceStable(keptUsers, func(i, j int) bool { return keptUsers[i].idx < keptUsers[j].idx })
	userTexts := make([]string, 0, len(keptUsers))
	for _, u := range keptUsers {
		userTexts = append(userTexts, u.text)
	}
	return userTexts
}

// adjacentProperNounUserLine recovers an immediately neighboring user turn
// naming a specific entity when the lone matched user turn names none.
func adjacentProperNounUserLine(s model.Session, anchor matchedScoredLine, asked string) (matchedScoredLine, bool) {
	if len(s.Messages) >= 60 || prompt.CarriesMidSentenceEntity(anchor.text) {
		return matchedScoredLine{}, false
	}
	for _, delta := range []int{-2, -1, 1, 2} {
		j := anchor.idx + delta
		if j < 0 || j >= len(s.Messages) {
			continue
		}
		m := s.Messages[j]
		if m.Role != "user" || noiseMessage(m.Text) || digest.IsCompactionSummary(strings.TrimSpace(m.Text)) ||
			digest.IsPlumbing(m.Text) || digest.IsToolCallRecord(m.Text) {
			continue
		}
		adj := firstLine(contextText(m.Text, false), 160)
		if adj != "" && !saysItHasNoMemory(adj) && !nearCopy(adj, asked) &&
			!nearCopy(adj, anchor.text) && prompt.CarriesMidSentenceEntity(adj) {
			return matchedScoredLine{idx: j, text: adj}, true
		}
	}
	return matchedScoredLine{}, false
}

func selectDiverseSecondUserLine(s model.Session, first matchedScoredLine, rest []matchedScoredLine, terms []string) (matchedScoredLine, bool) {
	var uncovered []string
	if len(terms) >= 3 {
		rawFirst := first.text
		if first.idx >= 0 && first.idx < len(s.Messages) {
			rawFirst = s.Messages[first.idx].Text
		}
		for _, t := range terms {
			if !TextCarriesTerm(first.text, t) && !TextCarriesTerm(rawFirst, t) {
				uncovered = append(uncovered, t)
			}
		}
	}
	bestIdx := -1
	bestCovered := 0
	for i, u := range rest {
		if redundantSecondUserLine(u.text, first.text) {
			continue
		}
		if len(uncovered) > 0 {
			covered := 0
			for _, t := range uncovered {
				if TextCarriesTerm(u.text, t) {
					covered++
				}
			}
			if covered > bestCovered {
				bestIdx = i
				bestCovered = covered
			}
			continue
		}
		if u.idx > first.idx && (digest.CarriesUserCorrection(u.text) || carriesConcreteQuantityOrUpdate(u.text)) {
			return u, true
		}
	}
	if bestIdx >= 0 {
		return rest[bestIdx], true
	}
	for _, u := range rest {
		if redundantSecondUserLine(u.text, first.text) {
			continue
		}
		if u.idx > first.idx && (digest.CarriesUserCorrection(u.text) || carriesConcreteQuantityOrUpdate(u.text)) {
			return u, true
		}
	}
	for _, u := range rest {
		if redundantSecondUserLine(u.text, first.text) {
			continue
		}
		if digest.CarriesUserCorrection(u.text) || carriesConcreteQuantityOrUpdate(u.text) {
			return u, true
		}
	}
	if !prompt.CarriesMidSentenceEntity(first.text) {
		for _, u := range rest {
			if !redundantSecondUserLine(u.text, first.text) && prompt.CarriesMidSentenceEntity(u.text) {
				return u, true
			}
		}
	}
	return matchedScoredLine{}, false
}

func redundantSecondUserLine(cand, first string) bool {
	if nearCopy(cand, first) {
		return true
	}
	wa := contentWords(cand)
	wb := contentWords(first)
	minLen := min(len(wa), len(wb))
	if minLen < 6 {
		return false
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	return float64(shared)/float64(minLen) >= 0.75
}

// concreteQuantityWords lists EN and RU numerals and quantity tokens that mark a
// concrete specification turn worth preserving alongside a match.
var concreteQuantityWords = map[string]bool{
	"zero": true, "one": true, "two": true, "three": true, "four": true,
	"five": true, "six": true, "seven": true, "eight": true, "nine": true,
	"ten": true, "eleven": true, "twelve": true,
	"single": true, "couple": true, "pair": true, "dozen": true, "solo": true,
	"half": true, "quarter": true, "several": true, "twice": true,
	"ноль": true, "нуль": true, "один": true, "одна": true, "одно": true, "одну": true,
	"два": true, "две": true, "двое": true, "три": true, "трое": true,
	"четыре": true, "пять": true, "шесть": true, "семь": true, "восемь": true,
	"девять": true, "десять": true, "одиннадцать": true, "двенадцать": true,
	"пара": true, "пару": true, "пары": true, "дюжина": true,
	"половина": true, "половину": true, "четверть": true,
	"несколько": true, "дважды": true, "трижды": true,
}

// concreteTransitionPhrases lists EN and RU state-update phrases that signal a
// settled change or current ownership fact.
var concreteTransitionPhrases = []string{
	"moved to", "moved back", "switched to", "changed to",
	"currently have", "currently own", "now have", "now own",
	"переехал в", "переехала в", "переехали в",
	"вернулся в", "вернулась в", "вернулись в",
	"перешёл на", "перешел на", "перешла на", "перешли на",
	"переключился на", "переключилась на",
	"сменил на", "сменила на", "поменял на", "поменяла на",
	"сейчас у меня", "теперь у меня", "сейчас есть", "теперь есть",
	"на данный момент",
}

func carriesConcreteQuantityOrUpdate(line string) bool {
	low := strings.ToLower(line)
	for _, phrase := range concreteTransitionPhrases {
		if strings.Contains(low, phrase) {
			return true
		}
	}
	fields := strings.FieldsFunc(low, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, tok := range fields {
		if concreteQuantityWords[tok] {
			return true
		}
		if utf8.RuneCountInString(tok) <= 6 && strings.ContainsAny(tok, "0123456789") {
			return true
		}
	}
	return false
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
