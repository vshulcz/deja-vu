package rulex

import (
	"slices"
	"testing"
)

func TestToLatinSpellsNames(t *testing.T) {
	cases := map[string]string{
		"нетаньяху": "netanyahu",
		"путин":     "putin",
		"щукин":     "shchukin",
		"юлия":      "yuliya",
		"чехов":     "chekhov",
		"цойка":     "tsoyka",
		"ёлкин":     "elkin",
		"съезд":     "sezd",
	}
	for in, want := range cases {
		if got := ToLatin(in); !slices.Contains(got, want) {
			t.Errorf("ToLatin(%q) = %q, want it to hold %q", in, got, want)
		}
	}
}

func TestToCyrillicSpellsNames(t *testing.T) {
	cases := map[string]string{
		"netanyahu": "нетаньяху",
		"putin":     "путин",
		"shchukin":  "щукин",
		"chekhov":   "чехов",
		"tsoy":      "цой",
		"zhukov":    "жуков",
	}
	for in, want := range cases {
		if got := ToCyrillic(in); !slices.Contains(got, want) {
			t.Errorf("ToCyrillic(%q) = %q, want it to hold %q", in, got, want)
		}
	}
}

func TestTranslitStaysSmallAndSkipsMixedTokens(t *testing.T) {
	if got := ToLatin("хохохохо"); len(got) > MaxCandidates {
		t.Errorf("candidates = %d, cap is %d", len(got), MaxCandidates)
	}
	if got := ToCyrillic("khakhakha"); len(got) > MaxCandidates {
		t.Errorf("candidates = %d, cap is %d", len(got), MaxCandidates)
	}
	for _, w := range []string{"", "ab", "лог", "k8sнастройки", "dict2"} {
		if got := ToLatin(w); got != nil {
			t.Errorf("ToLatin(%q) = %q, want none", w, got)
		}
	}
	if got := ToCyrillic("k8s"); got != nil {
		t.Errorf("ToCyrillic(k8s) = %q, want none", got)
	}
	if got := ToCyrillic("привет"); got != nil {
		t.Errorf("ToCyrillic of Cyrillic = %q, want none", got)
	}
	if got := ToLatin("hello"); got != nil {
		t.Errorf("ToLatin of Latin = %q, want none", got)
	}
}
