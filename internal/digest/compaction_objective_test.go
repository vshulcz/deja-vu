package digest

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The objective is the request being worked on. A status poll, a bare slash
// command and a notice the harness writes as a user turn all came out as the
// objective of real packets, with the actual request a few turns up.
func TestObjectiveSkipsTurnsThatAreNotARequest(t *testing.T) {
	const want = "померь, как агент тратит токены, и найди где экономить"
	for _, last := range []string{
		"ну что?",
		"ну что там?",
		"any update?",
		"/compact",
		"[ASYNC DELEGATION BATCH COMPLETE — deleg_ee9c50c2]\nA background fan-out of 3 subagent(s) finished.",
		"[IMPORTANT: Background process proc_12 completed normally.]",
		"[Your active task list was preserved across context compression]\n- [>] repo",
	} {
		s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
			{Role: "user", Text: want},
			{Role: "assistant", Text: "Запустил замер."},
			{Role: "user", Text: last},
		}}
		got := ExtractCompactionContext(s, ExtractOptions{}).Objective.Text
		if got != want {
			t.Errorf("after %q the objective is %q, want %q", last, got, want)
		}
	}
}

func TestObjectiveKeepsRealRequests(t *testing.T) {
	for _, req := range []string{
		"ну что, давай починим экспортер",
		"[выполни: ipconfig getifaddr en0]",
		"[29.06.2026 17:45] Vlad: коротко представься",
		"/review the parser change",
		"что там с CI на PR 12, почини если красный",
	} {
		s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
			{Role: "user", Text: "старая задача"},
			{Role: "user", Text: req},
		}}
		if got := ExtractCompactionContext(s, ExtractOptions{}).Objective.Text; got != req {
			t.Errorf("objective %q, want %q", got, req)
		}
	}
}
