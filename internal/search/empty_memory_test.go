package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A past session where the agent said it had no history, or was refused the
// tool that would have found some, is the worst thing a recall can quote: it
// hands the reader a failure of this very tool as if it were the answer.
// Measured on a real store, 3 blocks of 119 opened on one — twice for the same
// question about a CAN bus, once on a permission refusal.
func TestBlockDoesNotQuoteAnEmptyMemoryAdmission(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "напомни, что мы выясняли про can шину"},
		{Role: "assistant", Text: "I don't have any previous conversation context about the can bus work"},
		{Role: "assistant", Text: "в итоге can шину читаем через adb shell, скорость 500 кбит"},
	}}
	_, lines := matchedLinesAsked(s, []string{"can", "шину"}, "напомни, что мы выясняли про can шину")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	for _, ln := range lines {
		if strings.Contains(strings.ToLower(ln), "don't have any previous") {
			t.Errorf("the block quotes an admission that there was no memory: %q", ln)
		}
	}
	if !quotedAny(lines, "500") {
		t.Errorf("the line that actually answered was dropped: %q", lines)
	}
}

func TestPermissionRefusalIsNotQuotedEither(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с can шиной"},
		{Role: "assistant", Text: "Нужно разрешение на использование инструмента recall, чтобы найти детали про can шину"},
		{Role: "assistant", Text: "can шину в итоге читаем через adb, всё работает"},
	}}
	_, lines := matchedLinesAsked(s, []string{"can", "шину"}, "что там с can шиной")
	for _, ln := range lines {
		if strings.Contains(strings.ToLower(ln), "разрешение") {
			t.Errorf("a permission refusal was quoted as memory: %q", ln)
		}
	}
}

func quotedAny(lines []string, want string) bool {
	for _, ln := range lines {
		if strings.Contains(ln, want) {
			return true
		}
	}
	return false
}

// The conclusion slot takes lines from beside a match (#1493), and that path
// has its own filter: without it the admission comes back through the side
// door, since it is exactly the kind of line that follows a mention.
func TestTheReplySlotIgnoresAnEmptyMemoryAdmission(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 can \u0448\u0438\u043d\u043e\u0439"},
		{Role: "assistant", Text: "can \u0448\u0438\u043d\u0443 \u0441\u043c\u043e\u0442\u0440\u0435\u043b\u0438, can \u0430\u0434\u0430\u043f\u0442\u0435\u0440 \u043f\u043e\u0434\u043a\u043b\u044e\u0447\u0451\u043d"},
		{Role: "assistant", Text: "\u0432 \u0438\u0442\u043e\u0433\u0435 I don't have any previous conversation context about this"},
		{Role: "assistant", Text: "can \u0448\u0438\u043d\u0443 \u043f\u0440\u043e\u0432\u0435\u0440\u044f\u043b\u0438, can \u043b\u043e\u0433\u0438 \u0441\u043e\u0431\u0440\u0430\u043d\u044b"},
		{Role: "assistant", Text: "can \u0448\u0438\u043d\u0443 \u0447\u0438\u0442\u0430\u043b\u0438, can \u0442\u0440\u0430\u0444\u0438\u043a \u0432\u0438\u0434\u0435\u043d"},
	}}
	_, lines := matchedLinesAsked(s, []string{"can", "\u0448\u0438\u043d\u0443"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 can \u0448\u0438\u043d\u043e\u0439")
	for _, ln := range lines {
		if strings.Contains(strings.ToLower(ln), "don't have any previous") {
			t.Errorf("the reply slot quoted an empty-memory admission: %q", ln)
		}
	}
}

// A harness check is a conversation with the tool, not about the work: one
// message asks for an exact string back and the next repeats it. Such a session
// has nothing to recall, so none of it is shown — measured on a real store, 4
// blocks of 119 quoted one, and 11% of that store's sessions hold one, because
// the tool is developed against it.
func TestAHarnessCheckIsNotRecalledAtAll(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "smoke", Project: "proj",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "Reply with exactly: openclaw deja harness live test alpha"},
			{Role: "assistant", Text: "openclaw deja harness live test alpha"},
		},
	}
	if got := autoRecallSessionForAsked(s, time.Now(), true, []string{"harness"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 harness"); got != "" {
		t.Errorf("a harness check reached the block:\n%s", got)
	}
}

// Work that merely mentions the harness is ordinary work and stays.
func TestWorkAboutTheHarnessIsStillRecalled(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "real", Project: "proj",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 harness"},
			{Role: "assistant", Text: "\u0432 \u0438\u0442\u043e\u0433\u0435 harness \u043f\u043e\u0434\u043a\u043b\u044e\u0447\u0451\u043d \u0447\u0435\u0440\u0435\u0437 \u043f\u043b\u0430\u0433\u0438\u043d"},
		},
	}
	got := autoRecallSessionForAsked(s, time.Now(), true, []string{"harness"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 harness")
	if !strings.Contains(got, "\u043f\u043b\u0430\u0433\u0438\u043d") {
		t.Errorf("ordinary work about the harness was dropped:\n%s", got)
	}
}

// Each phrase carries its own weight: a check written as a search for the
// fixture string, without the "reply with exactly" wording, is the shape the
// live harness sweep actually leaves behind.
func TestAHarnessCheckWrittenAsASearchIsIgnored(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "smoke2", Project: "proj",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "search for openclaw deja harness live test alpha and name the harness"},
			{Role: "assistant", Text: "openclaw"},
		},
	}
	if got := autoRecallSessionForAsked(s, time.Now(), true, []string{"harness"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 harness"); got != "" {
		t.Errorf("a harness check reached the block:\n%s", got)
	}
}

// The same failure has more than one wording, and the first list caught one of
// them. Reading ten blocks the sweep counted as on topic turned up three more:
// the agent announcing it is about to look, that it looked and found nothing,
// or that it may not look at all. Measured after the first list, 4 blocks of
// 114 still opened on one of these.
func TestBlockDoesNotQuoteTheAgentAboutToLook(t *testing.T) {
	for _, line := range []string{
		"\u041f\u0440\u043e\u0432\u0435\u0440\u044e \u043f\u0430\u043c\u044f\u0442\u044c \u0438 \u0438\u0441\u0442\u043e\u0440\u0438\u044e \u043f\u0440\u043e\u0448\u043b\u044b\u0445 \u0441\u0435\u0441\u0441\u0438\u0439 \u043f\u043e can \u0448\u0438\u043d\u0435",
		"\u041c\u043d\u0435 \u043d\u0443\u0436\u043d\u043e \u0440\u0430\u0437\u0440\u0435\u0448\u0435\u043d\u0438\u0435 \u043d\u0430 \u0434\u043e\u0441\u0442\u0443\u043f \u043a \u043c\u043e\u0435\u0439 \u043f\u0430\u043c\u044f\u0442\u0438 \u043f\u0440\u043e can \u0448\u0438\u043d\u0443",
		"\u042f \u043d\u0435 \u043d\u0430\u0448\u0435\u043b \u0438\u043d\u0444\u043e\u0440\u043c\u0430\u0446\u0438\u044e \u043f\u0440\u043e can \u0448\u0438\u043d\u0443 \u0432 \u043c\u043e\u0435\u0439 \u043f\u0430\u043c\u044f\u0442\u0438",
	} {
		s := model.Session{Messages: []model.Message{
			{Role: "user", Text: "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 can \u0448\u0438\u043d\u043e\u0439"},
			{Role: "assistant", Text: line},
			{Role: "assistant", Text: "can \u0448\u0438\u043d\u0443 \u0432 \u0438\u0442\u043e\u0433\u0435 \u0447\u0438\u0442\u0430\u0435\u043c \u0447\u0435\u0440\u0435\u0437 adb, \u0441\u043a\u043e\u0440\u043e\u0441\u0442\u044c 500"},
		}}
		_, lines := matchedLinesAsked(s, []string{"can", "\u0448\u0438\u043d\u0443"},
			"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 can \u0448\u0438\u043d\u043e\u0439")
		for _, ln := range lines {
			if strings.Contains(ln, "\u043f\u0430\u043c\u044f\u0442") {
				t.Errorf("the block quotes the agent talking about its memory: %q", ln)
			}
		}
		if !quotedAny(lines, "500") {
			t.Errorf("the line that answered was dropped for %q: %q", line, lines)
		}
	}
}

// The other shape of a harness check: call the tool and answer in a fixed form.
// Measured on a real store, 2 blocks of 114 quoted one, and the "answer" one of
// them counted as was itself a check — the question set used to score the tool
// contains its own smoke tests.
func TestAScriptedToolCheckIsNotRecalled(t *testing.T) {
	for _, ask := range []string{
		"Call the deja recall MCP tool with query 'connection pool exhausted'. Answer one line: did it find anything",
		"\u0432\u044b\u0437\u043e\u0432\u0438 deja recall \u0441 \u0437\u0430\u043f\u0440\u043e\u0441\u043e\u043c 'connection pool exhausted', \u0438 \u0437\u0430\u043a\u043e\u043d\u0447\u0438 \u043e\u0442\u0432\u0435\u0442 \u0441\u043b\u043e\u0432\u043e\u043c \u0433\u043e\u0442\u043e\u0432\u043e",
	} {
		s := model.Session{
			Harness: "claude", ID: "scripted", Project: "proj",
			Updated: time.Now(),
			Messages: []model.Message{
				{Role: "user", Text: ask},
				{Role: "assistant", Text: "pgbouncer pool exhausted, \u0433\u043e\u0442\u043e\u0432\u043e"},
			},
		}
		if got := autoRecallSessionForAsked(s, time.Now(), true, []string{"pool"},
			"connection pool exhausted"); got != "" {
			t.Errorf("a scripted tool check reached the block:\n%s", got)
		}
	}
}

// Asking the tool to check its memory is ordinary work when the question is
// real: "проверь память deja, напомни, что мы решали про прокси" must survive.
func TestARealRequestToCheckMemorySurvives(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "real2", Project: "proj",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "\u043f\u0440\u043e\u0432\u0435\u0440\u044c \u043f\u0430\u043c\u044f\u0442\u044c deja, \u043d\u0430\u043f\u043e\u043c\u043d\u0438 \u043f\u0440\u043e \u043f\u0440\u043e\u043a\u0441\u0438"},
			{Role: "assistant", Text: "\u0432 \u0438\u0442\u043e\u0433\u0435 \u043f\u0440\u043e\u043a\u0441\u0438 \u0444\u0438\u043b\u044c\u0442\u0440\u0443\u0435\u043c \u043f\u043e quality score, \u043f\u043e\u0440\u043e\u0433 0.7"},
		},
	}
	got := autoRecallSessionForAsked(s, time.Now(), true, []string{"\u043f\u0440\u043e\u043a\u0441\u0438"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 \u043f\u0440\u043e\u043a\u0441\u0438")
	if !strings.Contains(got, "0.7") {
		t.Errorf("a real question about the work was filtered as a tool check:\n%s", got)
	}
}
