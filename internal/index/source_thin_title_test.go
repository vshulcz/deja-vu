package index

import (
	"strings"
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

// A sentence in a script that does not space its words is not thin: eight
// runes of Chinese say why the test failed, and a rune-length rule alone
// replaced it with a longer, worse turn (review of #3328).
func TestACJKTitleThatSaysSomethingIsNotThin(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "opencode", Project: "api", ID: "cjk1", Title: "为什么测试失败了",
		Messages: []model.Message{
			{Role: "user", Text: "please look into this in more detail and explain step by step what happened", Time: at},
		},
	}
	if got := metaForSession(s).Title; got != "为什么测试失败了" {
		t.Errorf("title = %q, want the store's own name", got)
	}
	// A greeting in the same script still is thin.
	s.Title = "你好"
	s.ID = "cjk2"
	if got := metaForSession(s).Title; got == "你好" {
		t.Errorf("title = %q, want the turn under a two-character greeting", got)
	}
}

// Korean writes its words apart, so a one-word acknowledgement is thin the way
// "ok" is, and a sentence in it is not (review of #3328).
func TestKoreanIsCountedByItsWordsNotItsSyllables(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "opencode", Project: "api", ID: "ko1", Title: "고마워",
		Messages: []model.Message{
			{Role: "user", Text: "the checkout worker drops connections under load", Time: at},
		},
	}
	if got := metaForSession(s).Title; got != "the checkout worker drops connections under load" {
		t.Errorf("title = %q, want the question — a one-word thanks names nothing", got)
	}
	s.Title = "결제 워커가 연결을 끊는 이유"
	s.ID = "ko2"
	if got := metaForSession(s).Title; got != "결제 워커가 연결을 끊는 이유" {
		t.Errorf("title = %q, want the store's own name — it is a whole sentence", got)
	}
}

// The turn that takes over must be something a person said. A resume preamble
// is the harness's, and notAsked has rejected it for the repeat counter all
// along (review of #3328).
func TestAWidenedTitleIsNeverTheHarnessOwnPreamble(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "deepseek", Project: "api", ID: "pre1", Title: "ok",
		Messages: []model.Message{
			{Role: "user", Text: "This session is being continued from a previous conversation that ran out of context. The conversation is summarized below:", Time: at},
			{Role: "user", Text: "cap the retries at three and log the last attempt", Time: at.Add(time.Minute)},
		},
	}
	got := metaForSession(s).Title
	if strings.Contains(got, "This session is being continued") {
		t.Errorf("title = %q, want the person's own turn", got)
	}
	if got != "cap the retries at three and log the last attempt" {
		t.Errorf("title = %q, want the sentence under the preamble", got)
	}
}

// The phrase a shell record uses is not a reason to reject a person's
// question: notAsked looks for "no visible output" anywhere in a turn, which
// is right for counting repeated plumbing and wrong for naming a session
// (second review of #3328).
func TestAQuestionThatSaysNoVisibleOutputCanStillNameTheSession(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 23, 0, 0, time.UTC)
	s := model.Session{
		Harness: "deepseek", Project: "api", ID: "nvo1", Title: "ok",
		Messages: []model.Message{
			{Role: "user", Text: "why does the button have no visible output when clicked, please debug", Time: at},
		},
	}
	// Cut to sixty runes like every widened title.
	if got := metaForSession(s).Title; got != "why does the button have no visible output when clicked, ple\u2026" {
		t.Errorf("title = %q, want the question", got)
	}
}
