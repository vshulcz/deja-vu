package digest

import "testing"

// A line that records a call being made is not a candidate for the slot that
// answers a question — it matches the question because it *is* the question,
// asked earlier by an agent whose stdout was captured (#2067).
func TestToolCallRecordIsNotSomethingSaid(t *testing.T) {
	records := []string{
		`claude  goprojects/deja-vu  > builder · gpt-5.6-luna ⚙ deja_recall {"query":"deja-vu repository description wording"}`,
		`⏺ Bash {"command":"go test ./..."}`,
		`▶ read_file { "path": "internal/index/index.go" }`,
	}
	for _, line := range records {
		if !IsToolCallRecord(line) {
			t.Errorf("a captured tool call read as something said: %q", line)
		}
	}

	// Prose about the same subjects stays a candidate. The marker alone is not
	// the shape — a status line uses one too.
	said := []string{
		"⚙ настройки переехали в конфиг, ключ читается один раз на старте",
		"we picked the second wording option for the repository description",
		"deja_recall returns the ranked sessions, not the raw store",
		"⏺ Bash закончился ошибкой, разбираю",
	}
	for _, line := range said {
		if IsToolCallRecord(line) {
			t.Errorf("prose barred as a tool call: %q", line)
		}
	}
}
