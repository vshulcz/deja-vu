package search

import (
	"strings"
	"testing"

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
