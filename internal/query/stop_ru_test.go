package query

import "testing"

// A Russian stop word is a family of forms: the list held "делай" and
// "покажи" but let "делали", "покажите", "посмотрите" and "давайте" through
// into the query as content words.
func TestRussianStopWordsAreMatchedByStem(t *testing.T) {
	for _, w := range []string{
		"делали", "делал", "делает", "покажите", "показать", "посмотрите",
		"давайте", "давать", "сделайте", "скажите", "сказать", "говорите",
		// the old surface list is untouched
		"делай", "покажи", "быть", "пожалуйста",
	} {
		if !IsStopWord(w) {
			t.Errorf("IsStopWord(%q) = false, want true", w)
		}
	}
}

// Stems are shared with content words, so the list must stay clear of them.
func TestRussianStopStemsSpareContentWords(t *testing.T) {
	for _, w := range []string{
		"дело", "дела", "делу", "показатель", "быт", "сказка", "сделка",
		"смотр", "лимит", "деплой", "limit", "show", "did",
	} {
		if w != "did" && IsStopWord(w) {
			t.Errorf("IsStopWord(%q) = true, want false", w)
		}
	}
	if !IsStopWord("did") {
		t.Error("English stop word did lost")
	}
}

func TestInflectedFillerIsDroppedFromTerms(t *testing.T) {
	terms, _ := QueryParts("покажите как делали деплой")
	if len(terms) != 1 || terms[0] != "деплой" {
		t.Fatalf("terms = %q, want [деплой]", terms)
	}
}
