package query

import (
	"slices"
	"testing"
)

func TestCJKTechnicalCompoundsSimplifiedAndTraditional(t *testing.T) {
	simp := CJKTechnicalCompounds("排查连接池在高并发下的内存泄漏和竞态条件")
	for _, want := range []string{"连接池", "内存泄漏", "竞态条件"} {
		if !slices.Contains(simp, want) {
			t.Errorf("CJKTechnicalCompounds(simp) = %v, missing %q", simp, want)
		}
	}

	trad := CJKTechnicalCompounds("排查連接池在高並發下的內存泄漏和競態條件")
	for _, want := range []string{"連接池", "內存泄漏", "競態條件"} {
		if !slices.Contains(trad, want) {
			t.Errorf("CJKTechnicalCompounds(trad) = %v, missing %q", trad, want)
		}
	}

	if got := CJKTechnicalCompounds("pure ascii query"); got != nil {
		t.Errorf("CJKTechnicalCompounds(ascii) = %v, want nil", got)
	}

	terms := RelevanceTerms("为什么连接池会出现内存泄漏？")
	for _, want := range []string{"连接池", "内存泄漏", "连接", "内存", "泄漏"} {
		if !slices.Contains(terms, want) {
			t.Errorf("RelevanceTerms = %v, missing %q", terms, want)
		}
	}
}
