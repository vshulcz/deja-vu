package rulex

import (
	"slices"
	"strings"
	"testing"
)

func TestRussianTermFindsItsLatinForms(t *testing.T) {
	for _, w := range []string{"деплой", "деплоя", "деплоем", "деплои"} {
		got := Alternatives(w)
		if !slices.Contains(got, "deploy") || !slices.Contains(got, "deployed") {
			t.Errorf("Alternatives(%q) = %q, want the deploy forms", w, got)
		}
	}
}

func TestLatinTermFindsItsRussianWords(t *testing.T) {
	got := Alternatives("deploy")
	if !slices.Contains(got, "деплой") || !slices.Contains(got, "деплоить") {
		t.Errorf("Alternatives(deploy) = %q", got)
	}
	if got := Alternatives("cache"); !slices.Contains(got, "кэш") || !slices.Contains(got, "кеш") {
		t.Errorf("both spellings of cache expected, got %q", got)
	}
}

func TestUnknownAndEmptyTermsHaveNoAlternatives(t *testing.T) {
	for _, w := range []string{"", "лимит", "zebra", "the"} {
		if got := Alternatives(w); got != nil {
			t.Errorf("Alternatives(%q) = %q, want none", w, got)
		}
	}
}

// Every row has both sides, one Russian word and at least one Latin form, all
// lower case, so a malformed row cannot silently drop out of the table.
func TestTableRowsAreWellFormed(t *testing.T) {
	rows := 0
	for _, line := range strings.Split(loanwordsTSV, "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		rows++
		ru, en, ok := strings.Cut(line, "\t")
		if !ok || ru == "" || en == "" || strings.ContainsAny(ru, " ,") || ru != strings.ToLower(ru) || en != strings.ToLower(en) {
			t.Errorf("malformed row %q", line)
		}
		if strings.ContainsRune(ru, 'ё') {
			t.Errorf("row %q: ё is folded to е before lookup", line)
		}
	}
	if rows < 40 || rows > 100 {
		t.Errorf("rows = %d, the table is meant to stay small and curated", rows)
	}
}
