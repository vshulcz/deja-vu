// Package rulex holds the Russian lexical rules that sit beside the stemmer:
// the IT loanwords Russian spells in Cyrillic and English in Latin (деплой and
// deploy), so a query in one script reaches a session written in the other.
// It sits over rustem and query.
package rulex

import (
	_ "embed"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/rustem"
)

//go:embed loanwords.tsv
var loanwordsTSV string

var (
	once    sync.Once
	byRoot  map[string][]string // Root of the Russian word -> Latin forms
	byLatin map[string][]string // Latin form -> Russian words
)

func load() {
	byRoot = map[string][]string{}
	byLatin = map[string][]string{}
	for _, line := range strings.Split(loanwordsTSV, "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		ru, en, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		ru = strings.TrimSpace(ru)
		forms := strings.Split(strings.TrimSpace(en), ",")
		root := rustem.Root(ru)
		for _, f := range forms {
			byRoot[root] = appendNew(byRoot[root], f)
			byLatin[f] = appendNew(byLatin[f], ru)
		}
	}
}

func appendNew(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// Alternatives returns the other-script spellings of a lower-case query term:
// the Latin forms for a Cyrillic term (any inflection of the Russian word), the
// Russian words for a Latin one. Nothing for a term the table does not know.
func Alternatives(term string) []string {
	once.Do(load)
	if term == "" {
		return nil
	}
	if query.IsRussian(term) {
		return byRoot[rustem.Root(term)]
	}
	return byLatin[term]
}
