package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session that never concludes anything gets no pairing, and two quotes then
// stop before the line that names what the reader needs.
func TestSessionWithoutAConclusionGetsThreeQuotes(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "three", Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "что там с pgbouncer"},
			{Role: "assistant", Text: "pgbouncer поднят на стейдже"},
			{Role: "assistant", Text: "pgbouncer смотрю логи"},
			{Role: "assistant", Text: "pgbouncer слушает порт 6432"},
			{Role: "assistant", Text: "pgbouncer и это всё на сегодня"},
		},
	}
	got := AutoRecallDigestForAsked([]model.Session{s}, 4096,
		[]string{"pgbouncer"}, "что там с pgbouncer")
	if !strings.Contains(got, "порт 6432") {
		t.Errorf("the third quote was dropped, and with it the only specific line:\n%s", got)
	}
	// And it stops at three: every further quote costs room a second session
	// would have taken.
	if strings.Contains(got, "это всё на сегодня") {
		t.Errorf("a fourth quote made it into the block:\n%s", got)
	}
}
