package search

import (
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
)

// LeadTermsKept is how many identifying query words lead-term matching judges a
// candidate session on.
const LeadTermsKept = 3

const (
	defaultPickerMaxMessages  = 300
	defaultPickerDigestBudget = 600
	hookIdentifierIDFFloor    = 4.0
	mediumStoreIDFFloor       = 2.5
	mediumSalientIDFFloor     = 2.2
	sameAnswerOverlap         = 0.8
	fingerprintTermsPerLine   = 2
)

// PromptPicker chooses up to two (or three across distinct dates) recalled
// sessions to inject for a prompt and tracks confidence, digest worthiness,
// and duplicate crowding metrics.
type PromptPicker struct {
	Pol          policy.Policy
	Terms        []string
	OrderedTerms []string
	Asked        string
	IDFOf        map[string]float64
	// TopIDF is the IDF a word spoken in one session reaches on this store, so a
	// small store's rarest words still identify a subject.
	TopIDF           float64
	Self             map[string]bool
	SelfProjectWords map[string]bool
	LeadTerms        []string
	Unscoped         bool
	CwdPlain         string
	Deadline         time.Time

	// MaxMessages caps session size before Focus narrowing runs; zero defaults
	// to 300.
	MaxMessages int
	// Focus narrows oversized sessions around Terms; nil leaves sessions whole.
	Focus func(model.Session, []string) model.Session
	// DigestBudget returns the digest character budget for a candidate; nil
	// defaults to 600.
	DigestBudget func(confident bool) int
	// ProjectMatches reports whether project matches any of terms.
	ProjectMatches func(project string, terms []string) bool

	picked       []model.Session
	confident    bool
	worthDigest  bool
	crowdedOut   int
	extraMatches int
}

// Picked returns the selected sessions in selection order.
func (p *PromptPicker) Picked() []model.Session { return p.picked }

// Confident reports whether the matched candidate set crossed the multi-term
// or paired-clause confidence threshold.
func (p *PromptPicker) Confident() bool { return p.confident }

// WorthDigest reports whether the picked sessions merit an inline digest body.
func (p *PromptPicker) WorthDigest() bool { return p.worthDigest }

// ExtraMatches returns how many additional distinct non-duplicate matches were
// seen after filling the display slots.
func (p *PromptPicker) ExtraMatches() int { return p.extraMatches }

// CrowdedOut returns how many candidates were rejected as duplicates, empty
// digests, or un-spoken lead matches.
func (p *PromptPicker) CrowdedOut() int { return p.crowdedOut }

// AddIDF merges unseen term IDF entries into p.IDFOf.
func (p *PromptPicker) AddIDF(extra map[string]float64) {
	if len(extra) == 0 {
		return
	}
	if p.IDFOf == nil {
		p.IDFOf = make(map[string]float64, len(extra))
	}
	for k, v := range extra {
		if _, exists := p.IDFOf[k]; !exists {
			p.IDFOf[k] = v
		}
	}
}

// Fork clones picker configuration for an unscoped named-project pass with
// fresh picked results and the supplied IDF table.
func (p *PromptPicker) Fork(unscoped bool, idf map[string]float64, terms []string) PromptPicker {
	cp := *p
	cp.Unscoped = unscoped
	cp.IDFOf = idf
	cp.OrderedTerms = ByIdentifying(terms, idf)
	cp.picked = make([]model.Session, 0, 2)
	cp.confident = false
	cp.worthDigest = false
	cp.crowdedOut = 0
	cp.extraMatches = 0
	return cp
}

// MergeUnscoped merges sessions selected from an unscoped project pass into p.
func (p *PromptPicker) MergeUnscoped(other *PromptPicker, terms []string) {
	if other == nil || len(other.picked) == 0 {
		return
	}
	for _, s := range LeadWithConclusion(other.picked, terms) {
		if len(p.picked) >= 3 || (len(p.picked) == 2 && !DistinctSessionDates(p.picked, s)) {
			if p.extraMatches < 5 {
				p.extraMatches++
			}
			continue
		}
		already := false
		for _, chosen := range p.picked {
			if chosen.ID == s.ID {
				already = true
				break
			}
		}
		if !SameAnswerAs(p.picked, s, terms) && !already {
			p.picked = append(p.picked, s)
		}
	}
	p.confident = p.confident || other.confident
	p.worthDigest = p.worthDigest || other.worthDigest
	p.extraMatches = min(p.extraMatches+other.extraMatches, 5)
}

func (p *PromptPicker) maxMessages() int {
	if p.MaxMessages <= 0 {
		return defaultPickerMaxMessages
	}
	return p.MaxMessages
}

func (p *PromptPicker) digestBudget() int {
	if p.DigestBudget != nil {
		return p.DigestBudget(p.confident)
	}
	return defaultPickerDigestBudget
}

func (p *PromptPicker) focusSession(s model.Session) model.Session {
	if p.Focus != nil {
		return p.Focus(s, p.Terms)
	}
	return s
}

func (p *PromptPicker) projectMatches(project string, terms []string) bool {
	if p.ProjectMatches != nil {
		return p.ProjectMatches(project, terms)
	}
	return false
}

func (p *PromptPicker) ordered() []string {
	if len(p.OrderedTerms) > 0 {
		return p.OrderedTerms
	}
	if len(p.Terms) > 0 {
		return ByIdentifying(p.Terms, p.IDFOf)
	}
	return ByIdentifying(p.LeadTerms, p.IDFOf)
}

func (p *PromptPicker) effectiveLeadTerms() []string {
	if len(p.LeadTerms) > 0 {
		return p.LeadTerms
	}
	ord := p.ordered()
	if len(ord) > LeadTermsKept {
		return ord[:LeadTermsKept]
	}
	return ord
}

// Pick walks ranked candidates and selects up to two (or three across distinct
// dates) sessions worth injecting.
func (p *PromptPicker) Pick(ranked []model.Session, matched, strong, naming []int) {
	strongNative := p.strongNativeTerms()
	maxMsgs := p.maxMessages()
	ordered := p.ordered()
	for i, s := range ranked {
		if len(p.picked) > 0 && !p.Deadline.IsZero() && time.Now().After(p.Deadline) {
			return
		}
		if !p.Pol.Allows(policy.ActivationAuto, s.Project) || s.Kind == "cron" || p.Self[s.ID] {
			continue
		}
		named := -1
		if i < len(naming) {
			named = naming[i]
		}
		whenRescued := p.whenClauseMatched(s)
		medRescued := p.mediumStoreWorth(s, matched[i]) || p.rareSubjectUpdateWorth(s, matched[i])
		if !RecallWorthShowingNaming(p.Terms, matched[i], strong[i], named, p.IDFOf) && !whenRescued && !medRescued {
			continue
		}
		p.markDigestWorth(s, matched[i], strong[i], named, whenRescued, medRescued, strongNative)
		if !p.LeadTermsMeet(s) {
			p.crowdedOut++
			continue
		}
		if len(s.Messages) > maxMsgs || s.Held > maxMsgs {
			s = p.focusSession(s)
			if len(s.Messages) == 0 {
				continue
			}
		}
		if SameAnswerAs(p.picked, s, p.Terms) {
			p.crowdedOut++
			continue
		}
		if _, shown := AutoRecallDigestShowing([]model.Session{s}, p.digestBudget(), ordered, p.Asked); len(shown) == 0 {
			p.crowdedOut++
			continue
		}
		if len(p.picked) < 2 {
			p.picked = append(p.picked, s)
			continue
		}
		if !whenRescued && !p.carriesPrimarySubjects(s) {
			continue
		}
		if len(p.picked) == 2 && DistinctSessionDates(p.picked, s) {
			p.picked = append(p.picked, s)
			continue
		}
		p.extraMatches++
		if p.extraMatches >= 5 {
			return
		}
	}
}

// TrimWeakThirdSession removes single-term distractors when at least two picked
// sessions carry two or more query terms together in speech.
func (p *PromptPicker) TrimWeakThirdSession() []model.Session {
	if len(p.picked) < 3 {
		return p.picked
	}
	multi := 0
	for _, s := range p.picked {
		if SpeechCarriesTermsTogether(s, p.Terms, 2) {
			multi++
		}
	}
	if multi < 2 {
		return p.picked
	}
	out := make([]model.Session, 0, len(p.picked))
	for _, s := range p.picked {
		if SpeechCarriesTermsTogether(s, p.Terms, 2) || p.whenClauseMatched(s) {
			out = append(out, s)
		}
	}
	return out
}

func (p *PromptPicker) carriesPrimarySubjects(s model.Session) bool {
	if len(p.picked) == 0 || prompt.IsDisjunctiveQuery(p.Asked) {
		return true
	}
	var primarySubjs []string
	hasSalient := false
	seen := make(map[string]bool, len(p.Terms))
	for _, t := range p.Terms {
		if seen[t] || prompt.IsFramingModifier(t) || p.IDFOf[t] < 1.0 {
			continue
		}
		seen[t] = true
		if SpeechCarriesAnyTerm(p.picked[0], []string{t}) {
			primarySubjs = append(primarySubjs, t)
			if p.IDFOf[t] >= mediumSalientIDFFloor {
				hasSalient = true
			}
		}
	}
	if !hasSalient || len(primarySubjs) < 2 {
		return true
	}
	covered := 0
	for _, t := range primarySubjs {
		if SpeechCarriesAnyTerm(s, []string{t}) {
			covered++
		}
	}
	return covered >= 2
}

func sessionTimestamp(s model.Session) time.Time {
	if !s.Updated.IsZero() {
		return s.Updated
	}
	return s.Started
}

// OrderDistinctDateUpdates puts newer distinct-date updates ahead of older
// sessions sharing a salient subject so latest state leads when no conclusion
// overrides ordering.
func OrderDistinctDateUpdates(ss []model.Session, terms []string, idfOf map[string]float64) []model.Session {
	if len(ss) < 2 || !HasDistinctDate(ss) {
		return ss
	}
	var subjs []string
	for _, t := range NonGenericRareTerms(terms, idfOf, mediumSalientIDFFloor) {
		if SpeechCarriesAnyTerm(ss[0], []string{t}) {
			subjs = append(subjs, t)
		}
	}
	if len(subjs) == 0 {
		return ss
	}
	for _, s := range ss[1:] {
		if !SpeechCarriesAnyTerm(s, subjs) {
			return ss
		}
	}
	ordered := slices.Clone(ss)
	sort.SliceStable(ordered, func(i, j int) bool {
		return sessionTimestamp(ordered[i]).After(sessionTimestamp(ordered[j]))
	})
	return ordered
}

func (p *PromptPicker) strongNativeTerms() []string {
	strongNative := IdentifyingTerms(p.Terms, p.IDFOf, p.TopIDF)
	if len(strongNative) == 0 {
		return nil
	}
	filtered := strongNative[:0]
	for _, t := range strongNative {
		low := strings.ToLower(t)
		if p.SelfProjectWords[low] || isHexHashLike(low) {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

func (p *PromptPicker) markDigestWorth(s model.Session, matched, strong, named int, whenRescued, medRescued bool, strongNative []string) {
	if len(p.picked) >= 2 {
		return
	}
	hasStrongNative := len(strongNative) > 0 && SpeechCarriesAnyTerm(s, strongNative)
	askedHit := AskedBefore(s, p.Terms) != ""
	shortAskedHit := askedHit && len(p.Terms) <= 6
	if named < 0 && !hasStrongNative && !askedHit && !whenRescued && !medRescued {
		return
	}
	if prompt.IsOpenAdviceQuery(p.Asked, p.Terms) && !shortAskedHit && !whenRescued {
		return
	}
	if matched >= 2 || whenRescued {
		p.confident = true
	}
	if named < 0 {
		soleCallerSubj := p.inCallerScope(s) && len(NonGenericRareTerms(p.Terms, p.IDFOf, 0)) == 1
		if (hasStrongNative && matched >= 2) || shortAskedHit || whenRescued || (medRescued && (matched >= 2 || (soleCallerSubj && matched >= 1))) {
			p.worthDigest = true
		}
		return
	}
	if matched >= 2 || whenRescued || medRescued || (strong >= 1 && (len(p.Terms) <= 2 || shortAskedHit || (hasStrongNative && (len(p.Terms) <= 4 || SpeechCarriesTermsTogether(s, p.Terms, 2))))) {
		p.worthDigest = true
	}
}

// LeadTermsMeet reports whether s speaks the query's identifying lead terms in
// user or assistant speech (requiring two together in oversized sessions).
func (p *PromptPicker) LeadTermsMeet(s model.Session) bool {
	maxMsgs := p.maxMessages()
	isLong := len(s.Messages) >= maxMsgs || s.Held >= maxMsgs
	if p.whenClauseMatched(s) {
		return true
	}
	if !isLong && p.inCallerScope(s) && len(p.picked) >= 1 && DistinctSessionDates(p.picked, s) &&
		UserCarriesRareSubjectUpdate(s, p.picked, p.Terms, p.IDFOf) {
		return true
	}
	if decided, match := p.fixedLeadTermsMeet(s, isLong); decided {
		return match
	}
	named := NonGenericRareTerms(NamedLeadTerms(p.ordered(), p.IDFOf, p.TopIDF), p.IDFOf, 0)
	if len(named) > 0 {
		need := named
		if len(need) > 2 {
			need = need[:2]
		}
		allCarry := true
		for _, t := range need {
			if !SpeechCarriesAnyTerm(s, []string{t}) && !p.projectMatches(s.Project, []string{t}) {
				allCarry = false
				break
			}
		}
		if allCarry {
			if len(need) >= 2 && isLong {
				return SpeechCarriesTermsTogether(s, need, 2)
			}
			return true
		}
		if len(named) >= 2 && !isLong && SpeechCarriesAnyTerm(s, named) {
			if rare := RareTerms(p.Terms, p.IDFOf, 2.5); len(rare) >= 2 && SpeechCarriesTermsTogether(s, rare, 2) {
				return true
			}
			if p.inCallerScope(s) && SpeechCarriesTermsTogether(s, NonGenericRareTerms(p.Terms, p.IDFOf, mediumSalientIDFFloor), 2) {
				return true
			}
			if prompt.IsDisjunctiveQuery(p.Asked) && p.inCallerScope(s) && SpeechCarriesTermsTogether(s, p.Terms, 2) {
				return true
			}
		}
	}
	if !isLong && p.mediumStoreSpeechMatch(s) {
		return true
	}
	if p.Unscoped {
		if p.CwdPlain != "" && strings.EqualFold(s.Project, p.CwdPlain) {
			if SpeechCarriesTermsTogether(s, p.Terms, 2) {
				return true
			}
			nonGen := NonGenericRareTerms(p.Terms, p.IDFOf, mediumSalientIDFFloor)
			if len(nonGen) == 1 && p.IDFOf[nonGen[0]] >= 2.8 && userCarriesTerm(s, nonGen[0]) {
				return true
			}
			if len(p.Terms) <= 6 {
				for _, t := range nonGen {
					if p.IDFOf[t] >= 3.0 && userCarriesTerm(s, t) {
						return true
					}
				}
			}
			return false
		}
		if len(named) == 0 && p.TopIDF >= 3.5 {
			if rare := RareTerms(p.Terms, p.IDFOf, 2.8); len(rare) >= 2 {
				return SpeechCarriesTermsTogether(s, rare, 2)
			}
		}
		return false
	}
	lead := p.effectiveLeadTerms()
	return SpeechCarriesTermsTogether(s, lead, 2) ||
		(p.projectMatches(s.Project, lead) && SpeechCarriesAnyTerm(s, lead))
}

// NonGenericRareTerms filters terms with IDF at or above floor while dropping
// temporal, aspectual, and light-verb framing modifiers.
func NonGenericRareTerms(terms []string, idfOf map[string]float64, floor float64) []string {
	var out []string
	for _, t := range terms {
		if !prompt.IsFramingModifier(t) && idfOf[t] >= floor {
			out = append(out, t)
		}
	}
	return out
}

func (p *PromptPicker) mediumStoreSpeechMatch(s model.Session) bool {
	if p.TopIDF < mediumStoreIDFFloor {
		return false
	}
	if p.TopIDF >= hookIdentifierIDFFloor &&
		len(NonGenericRareTerms(NamedLeadTerms(p.ordered(), p.IDFOf, p.TopIDF), p.IDFOf, 0)) > 0 {
		return false
	}
	nonGen := NonGenericRareTerms(p.Terms, p.IDFOf, mediumSalientIDFFloor)
	if len(nonGen) >= 2 && SpeechCarriesTermsTogether(s, nonGen, 2) {
		return true
	}
	if p.TopIDF < hookIdentifierIDFFloor && len(nonGen) >= 1 && len(p.Terms) <= 6 {
		nonGenPresent := NonGenericRareTerms(p.Terms, p.IDFOf, 1.0)
		if len(nonGenPresent) >= 2 && SpeechCarriesAnyTerm(s, nonGen) && SpeechCarriesTermsTogether(s, nonGenPresent, 2) {
			return true
		}
	}
	if prompt.IsDisjunctiveQuery(p.Asked) && p.inCallerScope(s) &&
		SpeechCarriesTermsTogether(s, p.Terms, 2) && SpeechCarriesAnyTerm(s, nonGen) {
		return true
	}
	if len(nonGen) == 1 && len(NonGenericRareTerms(p.Terms, p.IDFOf, 0)) == 1 && userCarriesTerm(s, nonGen[0]) {
		if p.IDFOf[nonGen[0]] >= 3.0 && p.inCallerScope(s) {
			return true
		}
		if p.IDFOf[nonGen[0]] >= max(mediumSalientIDFFloor, p.TopIDF-0.9) {
			return true
		}
	}
	return UserCarriesRareSubjectUpdate(s, p.picked, p.Terms, p.IDFOf)
}

func (p *PromptPicker) mediumStoreWorth(s model.Session, matched int) bool {
	maxMsgs := p.maxMessages()
	if matched < 1 || len(p.Terms) > 6 {
		return false
	}
	if len(s.Messages) >= maxMsgs || s.Held >= maxMsgs {
		return false
	}
	return p.mediumStoreSpeechMatch(s)
}

func (p *PromptPicker) rareSubjectUpdateWorth(s model.Session, matched int) bool {
	maxMsgs := p.maxMessages()
	if matched < 1 || len(p.Terms) > 6 || !p.inCallerScope(s) || len(p.picked) == 0 {
		return false
	}
	if len(s.Messages) >= maxMsgs || s.Held >= maxMsgs {
		return false
	}
	return DistinctSessionDates(p.picked, s) && UserCarriesRareSubjectUpdate(s, p.picked, p.Terms, p.IDFOf)
}

func (p *PromptPicker) inCallerScope(s model.Session) bool {
	return !p.Unscoped || (p.CwdPlain != "" && strings.EqualFold(s.Project, p.CwdPlain))
}

// HasScopedTarget reports whether the query is scoped either to explicit
// project names or to a non-empty working directory project.
func (p *PromptPicker) HasScopedTarget() bool {
	return !p.Unscoped || p.CwdPlain != ""
}

// ScopeFilterNames returns the project filter list to use for subject
// follow-up queries.
func (p *PromptPicker) ScopeFilterNames(names []string) []string {
	if !p.Unscoped {
		return names
	}
	if p.CwdPlain != "" {
		return []string{p.CwdPlain}
	}
	return nil
}

func userCarriesTerm(s model.Session, term string) bool {
	for _, m := range s.Messages {
		if m.Role == "user" && TextCarriesTerm(m.Text, term) {
			return true
		}
	}
	return false
}

// SkipWithPicked returns a copy of skip including the session IDs already in
// picked.
func SkipWithPicked(skip map[string]bool, picked []model.Session) map[string]bool {
	out := make(map[string]bool, len(skip)+len(picked))
	for id := range skip {
		out[id] = true
	}
	for _, s := range picked {
		out[s.ID] = true
	}
	return out
}

func (p *PromptPicker) fixedLeadTermsMeet(s model.Session, isLong bool) (bool, bool) {
	fixedNamed := NamedLeadTerms(p.ordered(), p.IDFOf, 1e9)
	if len(fixedNamed) < 2 {
		return false, false
	}
	fixedCheck := NonGenericRareTerms(fixedNamed, p.IDFOf, 0)
	if len(fixedCheck) < 2 {
		return false, false
	}
	sort.SliceStable(fixedCheck, func(i, j int) bool {
		return p.IDFOf[fixedCheck[i]] > p.IDFOf[fixedCheck[j]]
	})
	if len(fixedCheck) > 4 {
		fixedCheck = fixedCheck[:4]
	}
	allFixed := true
	for _, t := range fixedCheck {
		if !SpeechCarriesAnyTerm(s, []string{t}) && !p.projectMatches(s.Project, []string{t}) {
			allFixed = false
			break
		}
	}
	if allFixed {
		if isLong {
			return true, SpeechCarriesTermsTogether(s, fixedCheck, 2)
		}
		return true, true
	}
	if !isLong && prompt.IsDisjunctiveQuery(p.Asked) {
		ok := SpeechCarriesAnyTerm(s, fixedCheck) && SpeechCarriesTermsTogether(s, p.Terms, 2)
		return true, ok
	}
	return true, false
}

// SpokenSubjectTerms returns up to three non-framing query terms with IDF at
// or above minIDF that appear in speech in the first picked session.
func (p *PromptPicker) SpokenSubjectTerms(minIDF float64) []string {
	if len(p.picked) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(p.Terms))
	var out []string
	for _, t := range p.Terms {
		if seen[t] || prompt.IsFramingModifier(t) || p.IDFOf[t] < minIDF {
			continue
		}
		seen[t] = true
		if SpeechCarriesAnyTerm(p.picked[0], []string{t}) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return p.IDFOf[out[i]] > p.IDFOf[out[j]]
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// UserCarriesRareSubjectUpdate reports whether a user turn in s follows up on
// a non-framing rare subject shared with the first picked session.
func UserCarriesRareSubjectUpdate(s model.Session, picked []model.Session, terms []string, idfOf map[string]float64) bool {
	if len(picked) == 0 {
		return false
	}
	var pickedSubjs []string
	for _, t := range terms {
		if !prompt.IsFramingModifier(t) && idfOf[t] >= mediumSalientIDFFloor && SpeechCarriesAnyTerm(picked[0], []string{t}) {
			pickedSubjs = append(pickedSubjs, t)
		}
	}
	for _, subj := range terms {
		if prompt.IsFramingModifier(subj) || idfOf[subj] < mediumSalientIDFFloor {
			continue
		}
		if !SpeechCarriesAnyTerm(picked[0], []string{subj}) {
			continue
		}
		subjUserTurns := 0
		pairedInUserTurn := false
		assistantSpokeSubj := false
		properSubj := false
		for _, m := range picked[0].Messages {
			if prompt.MidSentenceEntityTerm(m.Text, subj) {
				properSubj = true
				break
			}
		}
		for _, m := range s.Messages {
			if !TextCarriesTerm(m.Text, subj) {
				continue
			}
			if !properSubj && prompt.MidSentenceEntityTerm(m.Text, subj) {
				properSubj = true
			}
			if m.Role == "assistant" {
				assistantSpokeSubj = true
				continue
			}
			if m.Role != "user" {
				continue
			}
			subjUserTurns++
			for _, other := range terms {
				if other != subj && TextCarriesTerm(m.Text, other) {
					pairedInUserTurn = true
					break
				}
			}
		}
		if len(pickedSubjs) >= 2 && !pairedInUserTurn && !SpeechCarriesTermsTogether(s, terms, 2) {
			continue
		}
		if subjUserTurns >= 1 && (idfOf[subj] >= 3.0 || (properSubj && idfOf[subj] >= 2.5) || subjUserTurns >= 2 || pairedInUserTurn || assistantSpokeSubj) {
			return true
		}
	}
	return false
}

// WhenClausePairTerms splits paired temporal or comparison questions into two
// subject-term clauses when both sides carry salient store terms.
func WhenClausePairTerms(asked string, allTerms []string, idfOf map[string]float64) ([]string, []string, bool) {
	if len(allTerms) < 4 {
		return nil, nil, false
	}
	leftRaw, rightRaw, isTemporal, ok := prompt.PairedClauseSplit(asked)
	if !ok {
		return nil, nil, false
	}
	if !isTemporal && !prompt.HasBetweenCue(asked) {
		return nil, nil, false
	}
	left := prompt.Terms(leftRaw)
	right := prompt.Terms(rightRaw)
	if len(left) < 2 || len(right) < 2 {
		return nil, nil, false
	}
	inAll := make(map[string]bool, len(allTerms))
	for _, t := range allTerms {
		inAll[t] = true
	}
	clauseKnown := func(side []string) bool {
		sideInAll := make([]string, 0, len(side))
		known, unknown := 0, 0
		for _, t := range side {
			if inAll[t] {
				sideInAll = append(sideInAll, t)
				if idfOf[t] > 0 {
					known++
				} else {
					unknown++
				}
			}
		}
		return len(NonGenericRareTerms(sideInAll, idfOf, mediumSalientIDFFloor)) >= 1 && (unknown == 0 || known >= 2)
	}
	if !clauseKnown(left) || !clauseKnown(right) {
		return nil, nil, false
	}
	return left, right, true
}

func sessionMatchesWhenClause(s model.Session, left, right []string, idfOf map[string]float64) int {
	carriesClause := func(clause []string) bool {
		if !SpeechCarriesTermsTogether(s, clause, 2) {
			return false
		}
		return SpeechCarriesAnyTerm(s, NonGenericRareTerms(clause, idfOf, mediumSalientIDFFloor))
	}
	if carriesClause(left) {
		return 1
	}
	if carriesClause(right) {
		return 2
	}
	return 0
}

func (p *PromptPicker) whenClauseMatched(s model.Session) bool {
	maxMsgs := p.maxMessages()
	if len(s.Messages) >= maxMsgs || s.Held >= maxMsgs {
		return false
	}
	left, right, ok := WhenClausePairTerms(p.Asked, p.Terms, p.IDFOf)
	if !ok {
		return false
	}
	return sessionMatchesWhenClause(s, left, right, p.IDFOf) != 0
}

// UncoveredWhenClause returns whichever side of a paired clause query is not
// yet represented by p's picked sessions.
func (p *PromptPicker) UncoveredWhenClause(left, right []string) []string {
	hasLeft, hasRight := false, false
	for _, picked := range p.picked {
		switch sessionMatchesWhenClause(picked, left, right, p.IDFOf) {
		case 1:
			hasLeft = true
		case 2:
			hasRight = true
		}
	}
	if !hasRight {
		return right
	}
	if !hasLeft {
		return left
	}
	return nil
}

// RareTerms returns the subset of terms whose IDF meets or exceeds floor.
func RareTerms(terms []string, idfOf map[string]float64, floor float64) []string {
	var out []string
	for _, t := range terms {
		if idfOf[t] >= floor {
			out = append(out, t)
		}
	}
	return out
}

// NamedLeadTerms returns identifying symbol-shaped or Latin-letter query
// terms, excluding pure digit tokens.
func NamedLeadTerms(terms []string, idfOf map[string]float64, topIDF float64) []string {
	var named []string
	for _, t := range IdentifyingTerms(terms, idfOf, topIDF) {
		if isAllDigitsWord(t) {
			continue
		}
		if SymbolShaped(t) || LatinWord(t) {
			named = append(named, t)
		}
	}
	return named
}

// LatinWord reports whether term contains at least one ASCII Latin letter.
func LatinWord(term string) bool {
	for _, r := range term {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// ByIdentifying orders terms by descending IDF, breaking ties by symbol shape,
// single-term identifier status, and rune length.
func ByIdentifying(terms []string, idf map[string]float64) []string {
	if len(idf) == 0 || len(terms) < 2 {
		return terms
	}
	out := append([]string(nil), terms...)
	sort.SliceStable(out, func(i, j int) bool {
		vi, oki := idf[out[i]]
		vj, okj := idf[out[j]]
		if vi != vj {
			return vi > vj
		}
		if oki != okj {
			return oki
		}
		if a, b := SymbolShaped(out[i]), SymbolShaped(out[j]); a != b {
			return a
		}
		if a, b := HasIdentifierTerm([]string{out[i]}), HasIdentifierTerm([]string{out[j]}); a != b {
			return a
		}
		return utf8.RuneCountInString(out[i]) > utf8.RuneCountInString(out[j])
	})
	return out
}

// SymbolShaped reports whether term carries punctuation or digits typical of
// symbols, paths, or versions.
func SymbolShaped(term string) bool {
	for _, r := range term {
		if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

func sessionDateTag(s model.Session) string {
	if s.Updated.IsZero() {
		return ""
	}
	return " (" + s.Updated.Local().Format("2006-01-02") + ")"
}

// SameAnswerAs reports whether s carries nearly identical matching-line wording
// to any session already in chosen.
func SameAnswerAs(chosen []model.Session, s model.Session, terms []string) bool {
	next := matchedWords(s, terms)
	if len(next) == 0 {
		return false
	}
	for _, c := range chosen {
		if sameWords(matchedWords(c, terms), next) {
			return true
		}
	}
	return false
}

// DistinctSessionDates reports whether s carries a non-empty local date not
// already present among ss.
func DistinctSessionDates(ss []model.Session, s model.Session) bool {
	w := sessionDateTag(s)
	if w == "" {
		return false
	}
	for _, existing := range ss {
		if sessionDateTag(existing) == w {
			return false
		}
	}
	return true
}

// HasDistinctDate reports whether shown spans at least two distinct non-empty
// local session dates.
func HasDistinctDate(shown []model.Session) bool {
	var seen []model.Session
	for _, s := range shown {
		if sessionDateTag(s) == "" {
			continue
		}
		if len(seen) > 0 && DistinctSessionDates(seen, s) {
			return true
		}
		seen = append(seen, s)
	}
	return false
}

func sameWords(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	shared := 0
	for w := range b {
		if a[w] {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	return union > 0 && float64(shared)/float64(union) >= sameAnswerOverlap
}

func matchedWords(s model.Session, terms []string) map[string]bool {
	words := map[string]bool{}
	for _, m := range s.Messages {
		fields := strings.Fields(strings.ToLower(m.Text))
		if TermHitsLowered(strings.Join(fields, " "), terms) < fingerprintTermsPerLine {
			continue
		}
		if !SpeechCarriesAnyTerm(model.Session{Messages: []model.Message{m}}, terms) {
			continue
		}
		for _, w := range fields {
			words[w] = true
		}
	}
	return words
}

func isHexHashLike(t string) bool {
	if len(t) < 6 || len(t) > 64 {
		return false
	}
	hasDigit := false
	for i := range len(t) {
		b := t[i]
		switch {
		case b >= '0' && b <= '9':
			hasDigit = true
		case b >= 'a' && b <= 'f':
		default:
			return false
		}
	}
	return hasDigit || len(t) >= 12
}

func isAllDigitsWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
