package search

import (
	"testing"
)

// A question can name something specific this store has never seen — work from
// another project, another machine, another life. The word still reads as a
// term of art, and that was enough: a single match on one of the ordinary words
// around it answered with whatever session says those words most often, which
// is whichever session is longest. The subject contributed nothing.
func TestASubjectTheStoreNeverSawCarriesNothing(t *testing.T) {
	terms := []string{"oauth", "rotation", "branch", "suite", "passed"}
	// The working vocabulary is here; the subject is not.
	known := map[string]float64{"branch": 2.4, "suite": 2.1}
	if RecallWorthShowing(terms, 1, 1, known) {
		t.Error("a question about something absent was answered on one match")
	}
	// Two ordinary words that met in one message is a different rule, and this
	// one leaves it alone.
	if !RecallWorthShowing(terms, 2, 1, known) {
		t.Error("the two-word rule was lost")
	}
	// Known is not enough on its own: the word also has to be rare here. The
	// working-word list is written by hand and "passed" is not on it, so shape
	// alone let it carry a match; the store says it is ordinary.
	ordinary := map[string]float64{"branch": 2.4, "suite": 2.1, "passed": 2.0}
	if RecallWorthShowing(terms, 1, 1, ordinary) {
		t.Error("a word the store calls ordinary carried a match on its shape")
	}
	// Rare here, and it carries — which is the rule this must not weaken.
	rare := map[string]float64{"branch": 2.4, "suite": 2.1, "passed": 5.1}
	if !RecallWorthShowing(terms, 1, 1, rare) {
		t.Error("a rare word the store holds stopped carrying a match")
	}
}

// The subject is present: one match on it is an answer, and that is the rule
// this must not weaken.
func TestAKnownSubjectStillCarriesOnItsOwn(t *testing.T) {
	terms := []string{"pgbouncer", "here"}
	known := map[string]float64{"pgbouncer": 4.2, "here": 1.0}
	if !RecallWorthShowing(terms, 1, 1, known) {
		t.Error("a rare word the store holds stopped carrying a match")
	}
}

// A short question is all subject. "как там pr, смержился?" names the thing and
// one working verb: there is nothing else in it a candidate could have matched
// instead, so it is judged on shape as before and the store is never asked.
func TestAShortQuestionIsJudgedOnShapeAlone(t *testing.T) {
	terms := []string{"смержился", "pr"}
	if !RecallWorthShowing(terms, 1, 1, map[string]float64{"pr": 3.0}) {
		t.Error("a short question naming its subject fell silent")
	}
}

// The rareness floor is the fixed one wherever the store can reach it, and the
// store's own ceiling where it cannot.
func TestIdentifyingTermsFloorFollowsTheStore(t *testing.T) {
	terms := []string{"zarvoxd", "cache", "absent"}
	big := map[string]float64{"zarvoxd": 4.3, "cache": 3.9}
	if got := IdentifyingTerms(terms, big, 7.5); len(got) != 1 || got[0] != "zarvoxd" {
		t.Errorf("a store that reaches the floor = %v, want [zarvoxd]", got)
	}
	// Two sessions: a word one of them says reaches ln(3/2), one both say 0.
	small := map[string]float64{"zarvoxd": 0.4054651081081644, "cache": 0}
	if got := IdentifyingTerms(terms, small, 0.4054651081081644); len(got) != 1 || got[0] != "zarvoxd" {
		t.Errorf("a small store = %v, want [zarvoxd]", got)
	}
	if got := IdentifyingTerms(terms, nil, 0); len(got) != len(terms) {
		t.Errorf("with no idf to judge by the question must stand: %v", got)
	}
}

// A question of three terms or fewer skipped the store check entirely — "a
// short question is all subject" — so `decide saltmarsh` asked where saltmarsh
// has never been said fired on `decide`. The ranking knows which of the
// question's words a session actually matched; the gate now asks (#3351).
func TestAShortQuestionNeedsTheSubjectToHaveMatched(t *testing.T) {
	terms := []string{"decide", "saltmarsh"}

	if RecallWorthShowingNaming(terms, 1, 0, 0, nil) {
		t.Error("fired on the working word beside the subject")
	}
	if !RecallWorthShowingNaming(terms, 1, 0, 1, nil) {
		t.Error("the session that matched the subject was refused")
	}
	// Nothing matched at all is still nothing.
	if RecallWorthShowingNaming(terms, 0, 0, 1, nil) {
		t.Error("fired on a session that matched none of the question")
	}
	// A caller with no count — the bridged retry ranks on neighbouring terms,
	// so counting the question's own words there would be a claim about a
	// query nobody asked — keeps the older rule.
	if !RecallWorthShowingNaming(terms, 1, 0, -1, nil) {
		t.Error("a ranking that cannot answer was treated as a refusal")
	}
	// And a question naming nothing is unchanged: two ordinary words earn it,
	// one does not.
	plain := []string{"build", "retry"}
	if RecallWorthShowingNaming(plain, 1, 0, 0, nil) {
		t.Error("one ordinary word fired")
	}
	if !RecallWorthShowingNaming(plain, 2, 0, 0, nil) {
		t.Error("two ordinary words did not")
	}
}

func TestRecallWorthShowing(t *testing.T) {
	if RecallWorthShowing([]string{"pgbouncer"}, 0, -1, nil) {
		t.Error("nothing matched, so there is nothing to show")
	}
	if !RecallWorthShowing([]string{"pgbouncer"}, 1, -1, nil) {
		t.Error("a word that identifies something stands on one match")
	}
	if RecallWorthShowing([]string{"build"}, 1, -1, nil) {
		t.Error("one ordinary word is not a subject on its own")
	}
	if !RecallWorthShowing([]string{"build", "retry"}, 2, -1, nil) {
		t.Error("two ordinary words that both matched earn the block")
	}
}

func TestHasIdentifierTerm(t *testing.T) {
	for _, term := range []string{"pgbouncer", "kafka", "сеть", "v11.2", "pkg/index", "bot_id", "死锁", "缓存", "数据库", "數據庫"} {
		if !HasIdentifierTerm([]string{term}) {
			t.Errorf("%q names something", term)
		}
	}
	for _, term := range []string{"build", "tests", "line", "of", "", "文件", "代码", "测试", "问题", "代碼"} {
		if HasIdentifierTerm([]string{term}) {
			t.Errorf("%q is ordinary vocabulary", term)
		}
	}
}
