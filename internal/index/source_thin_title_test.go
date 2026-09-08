package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A title the reader took from the store skipped the rule that a derived one
// has had since #790, so a harness whose title model answered with the reply
// left "ok" and "47" in every listing while the question sat one line below —
// 43 of the 60 sessions from three real stores on this machine (#3328).
func TestAThinSourceTitleGivesWayToTheQuestion(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "deepseek", Project: "dshproj", ID: "28341f5a", Title: "ok",
		Messages: []model.Message{
			{Role: "user", Text: "what did we settle about the checkout worker dropping connections", Time: at},
			{Role: "assistant", Text: "ok", Time: at.Add(time.Second)},
		},
	}
	// Cut to sixty runes like every derived title.
	if got := metaForSession(s).Title; got != "what did we settle about the checkout worker dropping connec\u2026" {
		t.Errorf("title = %q, want the person's question", got)
	}
}

// And it stands as it is when the session holds nothing better: the goose row
// titled "say ok" is honest, because "say ok" is the whole turn.
func TestAThinSourceTitleStaysWhenTheTurnIsThinToo(t *testing.T) {
	at := time.Date(2026, 7, 27, 18, 30, 0, 0, time.UTC)
	s := model.Session{
		Harness: "goose", Project: "deja-vu", ID: "20260727_2", Title: "say ok",
		Messages: []model.Message{
			{Role: "user", Text: "say ok", Time: at},
			{Role: "assistant", Text: "ok", Time: at.Add(time.Second)},
		},
	}
	if got := metaForSession(s).Title; got != "say ok" {
		t.Errorf("title = %q, want the name the store gave it", got)
	}
}

// A short title that names the work is not thin and is not touched.
func TestAShortSourceTitleThatNamesTheWorkIsKept(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "cline", Project: "api", ID: "c1", Title: "cap the retries",
		Messages: []model.Message{
			{Role: "user", Text: "the checkout worker drops connections under load, cap the retries", Time: at},
		},
	}
	if got := metaForSession(s).Title; got != "cap the retries" {
		t.Errorf("title = %q, want the store's own name", got)
	}
}
