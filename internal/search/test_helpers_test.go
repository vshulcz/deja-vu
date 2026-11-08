package search

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"regexp"
	"time"
)

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
