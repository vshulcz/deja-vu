package query

import (
	"strings"
	"testing"
)

func TestLowerFoldsYo(t *testing.T) {
	for _, in := range []string{"зелёный", "зеленый", "ЗЕЛЁНЫЙ", "Зелёный"} {
		if got := Lower(in); got != "зеленый" {
			t.Errorf("Lower(%q) = %q, want зеленый", in, got)
		}
	}
	if got := Lower("Hello"); got != "hello" {
		t.Errorf("Lower(Hello) = %q", got)
	}
	if len("ё") != len("е") {
		t.Fatal("fold must keep byte length")
	}
}

func TestTokensAndPartsFoldYo(t *testing.T) {
	got := Tokens("Зелёный ёж")
	if len(got) != 2 || got[0] != "зеленый" || got[1] != "еж" {
		t.Errorf("Tokens = %q", got)
	}
	_, phrases := QueryParts(`"всё течёт"`)
	if len(phrases) != 1 || phrases[0] != "все течет" {
		t.Errorf("phrases = %q", phrases)
	}
	if !MatchesQuery("Всё течёт, всё меняется", "все течет") {
		t.Error("ё text must match е query")
	}
	if !MatchesQuery("Все течет", "всё течёт") {
		t.Error("е text must match ё query")
	}
}

func TestRelevanceTermsFoldYo(t *testing.T) {
	got := RelevanceTerms("Зелёный отчёт")
	if len(got) != 2 || got[0] != "зеленый" || got[1] != "отчет" {
		t.Errorf("RelevanceTerms = %q", got)
	}
}

// «её» is a stop word written either way, also after the fold made it «ее».
func TestYoPronounStaysAStopWord(t *testing.T) {
	for _, q := range []string{"её отчёт", "ее отчет"} {
		terms, _ := QueryParts(q)
		if len(terms) != 1 || terms[0] != "отчет" {
			t.Errorf("QueryParts(%q) = %q", q, terms)
		}
	}
}

// Lower folds ё in the same pass as it lowercases, and gives what lowercasing
// and then replacing gives, invalid bytes included.
func TestLowerIsToLowerThenFold(t *testing.T) {
	for _, in := range []string{"", "ASCII Only", "Ёлка и ЁЖ", "Straße İstanbul ΣΑΣ", "a\xffб\xd1Ё", "ещё всё", "Ѐ Ѝ Ё ё"} {
		want := strings.ReplaceAll(strings.ToLower(in), "ё", "е")
		if got := Lower(in); got != want {
			t.Errorf("Lower(%q) = %q, want %q", in, got, want)
		}
	}
}

// A tokenizer folds rune by rune with LowerRune and must land where Lower does.
func TestLowerRuneIsLowerForOneRune(t *testing.T) {
	for _, r := range "AzЁёİΣKȺ中�" {
		if got, want := string(LowerRune(r)), Lower(string(r)); got != want {
			t.Errorf("LowerRune(%q) = %q, Lower gives %q", r, got, want)
		}
	}
}
