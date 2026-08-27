package query

import (
	"reflect"
	"testing"
)

// The tokenisation used to live in index, where every test of it went through
// a real search. It sits here now because prompt needs it too, so it gets a
// unit test at its own level: the search-level tests in index still run, and
// this one says what the function itself does with each kind of token.
func TestRelevanceTerms(t *testing.T) {
	cases := []struct {
		name string
		q    string
		want []string
	}{
		{"drops stop words and short tokens", "what is the ci job", []string{"job"}},
		{"keeps identifiers whole", "cmd/deja/hook_prompt.go failed", []string{"cmd/deja/hook_prompt.go", "failed"}},
		{"drops repeats", "release release workflow", []string{"release", "workflow"}},
		{"lowercases", "Windows RUNNER", []string{"windows", "runner"}},
		{"punctuation splits", "timeout: exit 137, retry?", []string{"timeout", "exit", "137", "retry"}},
		{"cyrillic survives", "почему сеть отваливается", []string{"почему", "сеть", "отваливается"}},
		{"cjk expands to bigrams, pure grammar dropped", "装订计数是什么", []string{"装订", "订计", "计数", "数是"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RelevanceTerms(c.q); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("RelevanceTerms(%q) = %q, want %q", c.q, got, c.want)
			}
		})
	}
}

// A single CJK rune is dropped like any other one-character token: the
// two-rune floor runs before the CJK exemption, which only spares two-byte
// tokens. ExpandCJKTokens still emits the unigram — that is for the index
// side, where it is a real key.
func TestRelevanceTermsDropsSingleCJKRune(t *testing.T) {
	if got := RelevanceTerms("车"); len(got) != 0 {
		t.Fatalf("RelevanceTerms(车) = %q, want none", got)
	}
}

func TestExpandCJKTokens(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"no cjk passes through", []string{"retry", "137"}, []string{"retry", "137"}},
		{"pure run becomes bigrams", []string{"装订计数"}, []string{"装订", "订计", "计数"}},
		{"single rune keeps its unigram", []string{"车"}, []string{"车"}},
		{"mixed token keeps its whole form too", []string{"v2装订"}, []string{"v2装订", "装订"}},
		{"a run never crosses a latin boundary", []string{"装订v2计数"}, []string{"装订v2计数", "装订", "计数"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExpandCJKTokens(c.in); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ExpandCJKTokens(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCJKFunctionBigram(t *testing.T) {
	cases := []struct {
		tok  string
		want bool
	}{
		{"什么", true},
		{"哪個", true},
		{"我點", true},
		{"装订", false},     // both runes carry content
		{"中国", false},     // 中 is a function rune alone, 国 is not
		{"的", false},      // one rune is not a bigram
		{"是retry", false}, // a latin rune is never grammar here
	}
	for _, c := range cases {
		if got := CJKFunctionBigram(c.tok); got != c.want {
			t.Fatalf("CJKFunctionBigram(%q) = %v, want %v", c.tok, got, c.want)
		}
	}
}
