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
// In real-world stores, 3 blocks of 119 opened on one — twice for the same
// question about a connection pool, once on a permission refusal.
func TestBlockDoesNotQuoteAnEmptyMemoryAdmission(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "напомни, что мы выясняли про pgbouncer пул"},
		{Role: "assistant", Text: "I don't have any previous conversation context about the pgbouncer pool work"},
		{Role: "assistant", Text: "в итоге pgbouncer пул держим через transaction mode, лимит 500 коннектов"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "пул"}, "напомни, что мы выясняли про pgbouncer пул")
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
		{Role: "user", Text: "что там с pgbouncer пулом"},
		{Role: "assistant", Text: "Нужно разрешение на использование инструмента recall, чтобы найти детали про pgbouncer пул"},
		{Role: "assistant", Text: "pgbouncer пул в итоге перевели на transaction mode, всё работает"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "пул"}, "что там с pgbouncer пулом")
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
		{Role: "user", Text: "что там с pgbouncer пулом"},
		{Role: "assistant", Text: "pgbouncer пул смотрели, пул адаптер подключён"},
		{Role: "assistant", Text: "в итоге I don't have any previous conversation context about this"},
		{Role: "assistant", Text: "pgbouncer пул проверяли, pgbouncer логи собраны"},
		{Role: "assistant", Text: "pgbouncer пул читали, pgbouncer трафик виден"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "пул"},
		"что там с pgbouncer пулом")
	for _, ln := range lines {
		if strings.Contains(strings.ToLower(ln), "don't have any previous") {
			t.Errorf("the reply slot quoted an empty-memory admission: %q", ln)
		}
	}
}

// A harness check is a conversation with the tool, not about the work: one
// message asks for an exact string back and the next repeats it. Such a session
// has nothing to recall, so none of it is shown — In real-world stores, 4
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
		"что там с harness"); got != "" {
		t.Errorf("a harness check reached the block:\n%s", got)
	}
}

// Work that merely mentions the harness is ordinary work and stays.
func TestWorkAboutTheHarnessIsStillRecalled(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "real", Project: "proj",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "что там с harness"},
			{Role: "assistant", Text: "в итоге harness подключён через плагин"},
		},
	}
	got := autoRecallSessionForAsked(s, time.Now(), true, []string{"harness"},
		"что там с harness")
	if !strings.Contains(got, "плагин") {
		t.Errorf("real work mentioning the harness was dropped:\n%s", got)
	}
}

// And the second shape of the same check, where the reply is not a verbatim
// echo.
func TestAHarnessSearchCheckIsNotRecalled(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "smoke2", Project: "proj",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "search for openclaw deja harness live test alpha and name the harness"},
			{Role: "assistant", Text: "openclaw"},
		},
	}
	if got := autoRecallSessionForAsked(s, time.Now(), true, []string{"harness"},
		"что там с harness"); got != "" {
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
		"Проверю память и историю прошлых сессий по pgbouncer пулу",
		"Мне нужно разрешение на доступ к моей памяти про pgbouncer пул",
		"Я не нашел информацию про pgbouncer пул в моей памяти",
	} {
		s := model.Session{Messages: []model.Message{
			{Role: "user", Text: "что там с pgbouncer пулом"},
			{Role: "assistant", Text: line},
			{Role: "assistant", Text: "pgbouncer пул в итоге держим через transaction mode, лимит 500"},
		}}
		_, lines := matchedLinesAsked(s, []string{"pgbouncer", "пул"},
			"что там с pgbouncer пулом")
		for _, ln := range lines {
			if strings.Contains(ln, "памят") {
				t.Errorf("the block quotes the agent talking about its memory: %q", ln)
			}
		}
		if !quotedAny(lines, "500") {
			t.Errorf("the line that answered was dropped for %q: %q", line, lines)
		}
	}
}

// The other shape of a harness check: call the tool and answer in a fixed form.
// In real-world stores, 2 blocks of 114 quoted one, and the "answer" one of
// them counted as was itself a check — the question set used to score the tool
// contains its own smoke tests.
func TestAScriptedToolCheckIsNotRecalled(t *testing.T) {
	for _, ask := range []string{
		"Call the deja recall MCP tool with query 'connection pool exhausted'. Answer one line: did it find anything",
		"вызови deja recall с запросом 'connection pool exhausted', и закончи ответ словом готово",
	} {
		s := model.Session{
			Harness: "claude", ID: "scripted", Project: "proj",
			Updated: time.Now(),
			Messages: []model.Message{
				{Role: "user", Text: ask},
				{Role: "assistant", Text: "pgbouncer pool exhausted, готово"},
			},
		}
		if got := autoRecallSessionForAsked(s, time.Now(), true, []string{"pool"},
			"connection pool exhausted"); got != "" {
			t.Errorf("a scripted tool check reached the block:\n%s", got)
		}
	}
}

// Asking the tool to check its memory is ordinary work when the question is
// real: "проверь память deja, напомни про балансировщик" must survive.
func TestARealRequestToCheckMemorySurvives(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "real2", Project: "proj",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "проверь память deja, напомни про балансировщик"},
			{Role: "assistant", Text: "в итоге балансировщик ограничиваем по retry budget, порог 0.7"},
		},
	}
	got := autoRecallSessionForAsked(s, time.Now(), true, []string{"балансировщик"},
		"что там с балансировщиком")
	if !strings.Contains(got, "0.7") {
		t.Errorf("a real question about the work was filtered as a tool check:\n%s", got)
	}
}
