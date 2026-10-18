package prompt

import (
	"reflect"
	"testing"
)

// A Latin identifier glued to a Russian word (k8sнастройки) is two terms; the
// hook used to reject the whole token because it is neither ASCII nor Cyrillic.
func TestGluedLatinLedTokenIsSplit(t *testing.T) {
	got := Terms("k8sнастройки кластера")
	want := map[string]bool{"k8s": true, "настройки": true, "кластера": true}
	for _, term := range got {
		if !want[term] {
			t.Errorf("unexpected term %q in %q", term, got)
		}
		delete(want, term)
	}
	for term := range want {
		t.Errorf("missing term %q in %q", term, got)
	}
}

// A split is for a token that glues the two scripts: plain English, versions
// and Russian compounds are left as they were, and деплой-k8s is two words.
func TestSplittingLeavesOtherTokensAlone(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"kubernetes-config v1.2.3 rate_limit", []string{"kubernetes-config", "v1.2.3", "rate_limit"}},
		{"деплой-k8s", []string{"деплой", "k8s"}},
		{"лог-сообщение", []string{"лог-сообщение"}},
	} {
		got := Terms(tc.in)
		for _, w := range tc.want {
			found := false
			for _, g := range got {
				if g == w {
					found = true
				}
			}
			if !found {
				t.Errorf("Terms(%q) = %q, missing %q", tc.in, got, w)
			}
		}
	}
}

// ё and е are one letter to the index, so a prompt that says both spends one
// term, written as е.
func TestYoFoldsAndFormsShareOneTerm(t *testing.T) {
	got := Terms("зелёный отчёт зеленый отчет")
	if !reflect.DeepEqual(got, []string{"зеленый", "отчет"}) {
		t.Fatalf("Terms = %q, want [зеленый отчет]", got)
	}
	got = Terms("лимит лимиты лимитов запросов")
	if !reflect.DeepEqual(got, []string{"лимит", "запросов"}) {
		t.Fatalf("forms of one word spent several terms: %q", got)
	}
}
