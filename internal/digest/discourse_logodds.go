package digest

import (
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	// logOddsDirichletAlpha0 is the prior concentration parameter alpha_0 in
	// Monroe, Colaresi & Quinn (2008), "Fightin' Words: Lexical Feature
	// Selection and Evaluation for Identifying the Content of Political
	// Conflict".
	logOddsDirichletAlpha0 = 16.0
	// logOddsMinZ is the 95% confidence threshold (z >= 1.96) for a token to
	// be accepted as a statistically significant outcome marker.
	logOddsMinZ = 1.96
	// logOddsMinPosCount is the minimum number of trajectory-verified positive
	// turns a word must appear in before it can be learned.
	logOddsMinPosCount = 3
	// logOddsMaxMarkers caps the learned discourse vocabulary size.
	logOddsMaxMarkers = 64
)

var learnedDecisionMarkers atomic.Pointer[[]string]

// SetLearnedDecisionMarkers installs the corpus-learned outcome markers
// discovered via Monroe et al. (2008) Dirichlet-smoothed log-odds.
func SetLearnedDecisionMarkers(markers []string) {
	if len(markers) == 0 {
		learnedDecisionMarkers.Store(nil)
		return
	}
	cp := append([]string(nil), markers...)
	learnedDecisionMarkers.Store(&cp)
}

// LearnedDecisionMarkers returns the currently installed corpus-learned
// discourse markers.
func LearnedDecisionMarkers() []string {
	p := learnedDecisionMarkers.Load()
	if p == nil {
		return nil
	}
	return *p
}

// LearnDiscourseMarkers mines outcome-signaling words from ss using
// self-supervised trajectory labels and Monroe et al. (2008) Log-Odds Ratio
// with an Informative Dirichlet Prior:
//
//   - Positive class (C+): opening sentences of turn-final assistant syntheses
//     in turns that performed a file edit/write (RoleEdit/RoleWrote) or ran a
//     passing command (RoleCommand → exit 0).
//   - Negative class (C-): mid-turn exploration preambles before RoleFiles or
//     RoleCommand (prior to any edit), plus assistant turns that neither edited
//     nor ran a passing command.
//
// Domain nouns that appear in both exploration and resolution cancel out
// (z ~ 0), while true discourse outcome verbs/markers in any language
// (EN, RU, ZH, JA) achieve z >= 1.96.
func LearnDiscourseMarkers(ss []model.Session) []string {
	posCounts := map[string]int{}
	negCounts := map[string]int{}
	nPos, nNeg := 0, 0

	for _, s := range ss {
		var sawEdit, sawPassCmd bool
		msgs := s.Messages
		for i, m := range msgs {
			observeTrajectoryStep(m, &sawEdit, &sawPassCmd)
			if m.Role != "assistant" {
				continue
			}
			text := strings.TrimSpace(m.Text)
			if text == "" || IsAgentArtifact(text) || IsCompactionSummary(text) {
				continue
			}
			lead := strings.TrimSpace(firstSentences(text, 2))
			if lead == "" {
				continue
			}
			switch {
			case verifiedTurnSynthesis(msgs, i, text, sawEdit, sawPassCmd):
				nPos++
				for _, w := range discourseCandidateWords(lead) {
					posCounts[w]++
				}
			case precedesExplorationAction(msgs, i, sawEdit) || (!sawEdit && !sawPassCmd && isTurnFinalAssistant(msgs, i)):
				nNeg++
				for _, w := range discourseCandidateWords(lead) {
					negCounts[w]++
				}
			}
		}
	}
	if nPos < logOddsMinPosCount || nNeg < logOddsMinPosCount {
		return nil
	}

	type scoredWord struct {
		w string
		z float64
		c int
	}
	var scored []scoredWord
	totalPosTokens := 0
	for _, c := range posCounts {
		totalPosTokens += c
	}
	totalNegTokens := 0
	for _, c := range negCounts {
		totalNegTokens += c
	}
	if totalPosTokens == 0 || totalNegTokens == 0 {
		return nil
	}
	totalTokens := float64(totalPosTokens + totalNegTokens)
	fPos := float64(totalPosTokens)
	fNeg := float64(totalNegTokens)

	for w, yPos := range posCounts {
		if yPos < logOddsMinPosCount {
			continue
		}
		yNeg := negCounts[w]
		// Informative Dirichlet prior proportional to background multinomial
		// token frequency (Monroe, Colaresi & Quinn 2008, eq. 16-18).
		alphaW := logOddsDirichletAlpha0 * float64(yPos+yNeg) / totalTokens
		if alphaW < 0.01 {
			alphaW = 0.01
		}
		numPos := float64(yPos) + alphaW
		denPos := math.Max(fPos+logOddsDirichletAlpha0-numPos, 0.5)
		numNeg := float64(yNeg) + alphaW
		denNeg := math.Max(fNeg+logOddsDirichletAlpha0-numNeg, 0.5)

		delta := math.Log(numPos/denPos) - math.Log(numNeg/denNeg)
		sigma := math.Sqrt(1.0/numPos + 1.0/numNeg)
		if sigma <= 0 {
			continue
		}
		z := delta / sigma
		if z >= logOddsMinZ {
			scored = append(scored, scoredWord{w: w, z: z, c: yPos})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		if math.Abs(scored[i].z-scored[j].z) > 1e-9 {
			return scored[i].z > scored[j].z
		}
		if scored[i].c != scored[j].c {
			return scored[i].c > scored[j].c
		}
		return scored[i].w < scored[j].w
	})
	if len(scored) > logOddsMaxMarkers {
		scored = scored[:logOddsMaxMarkers]
	}
	out := make([]string, len(scored))
	for i, sw := range scored {
		out[i] = sw.w
	}
	return out
}

func isTurnFinalAssistant(msgs []model.Message, i int) bool {
	for j := i + 1; j < len(msgs); j++ {
		if msgs[j].Role == "user" {
			return true
		}
		if msgs[j].Role == sources.RoleEdit || msgs[j].Role == sources.RoleWrote ||
			msgs[j].Role == sources.RoleCommand || msgs[j].Role == sources.RoleFiles ||
			(msgs[j].Role == "assistant" && strings.TrimSpace(msgs[j].Text) != "") {
			return false
		}
	}
	return true
}

var discourseStopWords = map[string]bool{
	"with": true, "from": true, "that": true, "this": true, "when": true,
	"which": true, "both": true, "into": true, "against": true, "every": true,
	"here": true, "there": true, "where": true, "while": true, "after": true,
	"before": true, "under": true, "over": true, "between": true, "through": true,
	"about": true, "these": true, "those": true, "their": true, "other": true,
	"only": true, "also": true, "more": true, "most": true, "some": true,
	"such": true, "than": true, "then": true, "were": true, "have": true,
	"been": true, "does": true, "make": true, "made": true, "used": true,
	"using": true, "uses": true,
	"было": true, "будет": true, "один": true, "одна": true, "одно": true,
	"если": true, "чтобы": true, "через": true, "между": true, "после": true,
	"перед": true, "когда": true, "только": true, "также": true, "этого": true,
	"этом": true, "этой": true, "всех": true, "всего": true,
}

func discourseCandidateWords(sent string) []string {
	// Strip backticked code spans so code identifiers are not learned as
	// general discourse markers.
	var clean strings.Builder
	inTick := false
	for i := range len(sent) {
		if sent[i] == '`' {
			inTick = !inTick
			clean.WriteByte(' ')
			continue
		}
		if !inTick {
			clean.WriteByte(sent[i])
		}
	}
	low := strings.ToLower(clean.String())
	if cjkfold.HasCJK(low) {
		low = cjkfold.String(low)
	}
	seen := map[string]bool{}
	var out []string
	for raw := range strings.FieldsSeq(low) {
		w := strings.TrimFunc(raw, func(r rune) bool {
			return !unicode.IsLetter(r)
		})
		if w == "" || discourseStopWords[w] || looksLikeAPath(raw) || strings.ContainsAny(raw, "/._-0123456789") {
			continue
		}
		nRunes := utf8.RuneCountInString(w)
		if nRunes < 4 || nRunes > 20 || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func carriesLearnedDecision(low, rawText string, asked []string) bool {
	markers := LearnedDecisionMarkers()
	if len(markers) == 0 || !carriesTechnicalEntity(rawText) {
		return false
	}
	for _, m := range markers {
		if !marksLine(low, m) {
			continue
		}
		skip := false
		for _, a := range asked {
			if a != "" && strings.Contains(m, cjkfold.String(strings.ToLower(a))) {
				skip = true
				break
			}
		}
		if !skip {
			return true
		}
	}
	return false
}
