package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
)

// The relevance excerpt asks which of a query's terms each message holds. A
// Russian word arrives as forty surface forms, and every one of them used to be
// searched for over every message (#5378); the forms are now checked where
// their stem was found, and the answer must be the one a search per term gives.
func TestTermSetMatchesASearchPerTerm(t *testing.T) {
	stems := []string{"сервер", "ошибк", "мост", "при"}
	suffixes := []string{"", "а", "у", "ом", "ами", "ах", "ов", "е", "и", "ы", "ей", "ями"}
	var terms []string
	for _, s := range stems {
		for _, x := range suffixes {
			terms = append(terms, s+x)
		}
	}
	terms = append(terms, "ошибка", "", "mostly", "数据库", "服务")
	texts := []string{
		"",
		"сервера упали, ошибками завален лог",
		"при приеме: самостоятельно мостами и мостов",
		"серверсервером сервер",
		"ошибк ошибкиями",
		"нет совпадений вообще",
		"数据库连接失败 mostly",
		"приприприями",
	}
	set := newTermSet(terms)
	found := make([]bool, len(terms))
	var pos []int
	for _, text := range texts {
		low := lower(text)
		pos, _ = set.match(low, found, pos)
		folded := cjkfold.String(low)
		for i, term := range terms {
			want := strings.Contains(low, term)
			if ft := cjkfold.String(term); !want && (ft != term || cjkfold.HasCJK(term)) {
				want = strings.Contains(folded, ft)
			}
			if found[i] != want {
				t.Errorf("%q in %q: termSet says %v, a search says %v", term, text, found[i], want)
			}
		}
	}
}

// One search over the text per stem, not per form: a message that does not
// hold «сервер» is not searched again for «серверами».
func TestTermSetSearchesOncePerStem(t *testing.T) {
	var terms []string
	for _, stem := range []string{"сервер", "кластер", "запрос"} {
		for _, x := range []string{"", "а", "у", "ом", "ами", "ах", "ов", "е", "ы"} {
			terms = append(terms, stem+x)
		}
	}
	set := newTermSet(terms)
	found := make([]bool, len(terms))
	_, probes := set.match(lower("ничего общего с вопросом"), found, nil)
	if probes != 3 {
		t.Errorf("%d terms over a message holding none of them took %d searches, want 3", len(terms), probes)
	}
}
