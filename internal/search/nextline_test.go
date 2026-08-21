package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A message names the subject on one line and settles it on the next. Quoting
// only the line that matched hands the reader the mention without the answer.
func TestQuoteCarriesTheLineAfterTheMatch(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "next", Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "что там с pgbouncer"},
			{Role: "assistant", Text: "смотрел pgbouncer\nв итоге держим 40 коннектов на шард\nдальше не трогали"},
			{Role: "user", Text: "ок"},
			{Role: "assistant", Text: "ага"},
		},
	}
	got := AutoRecallDigestForAsked([]model.Session{s}, 4096,
		[]string{"pgbouncer"}, "что там с pgbouncer")
	if !strings.Contains(got, "40 коннектов на шард") {
		t.Errorf("the quote stopped at the mention and left out the line that answers it:\n%s", got)
	}
}
