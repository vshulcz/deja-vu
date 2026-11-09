package search

import (
	"io"
	"regexp"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

func PrintSession(w io.Writer, s model.Session) {
	PrintSessionStyled(w, s, false, 0)
}

func scoreBM25(documents []bm25Document, df []int, corpusDocuments int, avgLength float64, queryTokenCount int, worn map[string]int) []Hit {
	return scoreBM25At(documents, df, corpusDocuments, avgLength, queryTokenCount, worn, (Options{}).Clock())
}

func autoRecallSession(s model.Session, now time.Time, provenance bool) string {
	return autoRecallSessionFor(s, now, provenance, nil)
}

func autoRecallSessionFor(s model.Session, now time.Time, provenance bool, terms []string) string {
	return autoRecallSessionForAsked(s, now, provenance, terms, "")
}

var lineNumberRE = regexp.MustCompile(`^\s*\d{1,5}[:|]\s+`)

// AutoRecallDigest renders the session-start block within budget.
func AutoRecallDigest(ss []model.Session, budget int) string {
	return AutoRecallDigestFor(ss, budget, nil)
}

// IsStopWord is query.IsStopWord.
func IsStopWord(term string) bool { return query.IsStopWord(term) }

// MatchesQuery is query.MatchesQuery.
func MatchesQuery(text, q string) bool {
	terms, phrases := query.QueryParts(q)
	return query.MatchesParts(text, terms, phrases, nil)
}

// MatchesParts is query.MatchesParts.
func MatchesParts(text string, terms, phrases []string, variants map[string][]string) bool {
	return query.MatchesParts(text, terms, phrases, variants)
}
