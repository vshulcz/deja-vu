package digest

import "testing"

// The shapes below are the first prompts of real switches between harnesses on
// one machine, and the near misses beside them.
func TestHandoffIntentReadsOnlyAnExplicitAsk(t *testing.T) {
	for _, tc := range []struct {
		prompt  string
		ok      bool
		id      string
		harness string
	}{
		{"перенеси в opencode сессию claude --resume session_01ABCdefGHIjklMNOpq", true, "session_01ABCdefGHIjklMNOpq", ""},
		{"Перенос продолжения с локальной OpenCode-сессии. Не коммить и не пушить ничего.", true, "", "opencode"},
		{"continue the codex session we had on the parser", true, "", "codex"},
		{"pick up where https://claude.ai/code/session_01ABCdefGHIjklMNOpq left off", true, "session_01ABCdefGHIjklMNOpq", ""},
		// The commonest turn there is.
		{"продолжай", false, "", ""},
		{"continue with the parser fix", false, "", ""},
		// A command line resuming a session, inside a request about that session.
		{"надо в сессии claude --resume 3f2a9c1e-0b7d-4c55-9e21-5d0c7a6b8e43 удалить два последних сообщения", false, "", ""},
		// A search for a session, with "continue" a sentence away.
		{"у меня где то была сессия claude про дистрибуцию, найди где она находится, мне нужно в нее зайти и потом через какое-то время продолжить", false, "", ""},
		// A package deja already wrote.
		{"You are picking up work handed off from a opencode session (project app). Continue from there.", false, "", ""},
		{"continue the session we had in cursor on the parser", true, "", "cursor"},
		{"resume cursor's session about the parser", true, "", "cursor"},
		// A harness name that is an ordinary word, away from the session word.
		{"resume work on session pagination: the cursor is lost after page 2", false, "", ""},
		{"continue the session cleanup, the hermes build kept failing", false, "", ""},
		// A date and a version are not ids.
		{"continue the release 2026-09-19 v0.21.5", false, "", ""},
	} {
		ask, ok := HandoffIntent(tc.prompt)
		if ok != tc.ok {
			t.Errorf("%q: ok=%v, want %v (%+v)", tc.prompt, ok, tc.ok, ask)
			continue
		}
		if !ok {
			continue
		}
		if tc.id != "" && (len(ask.IDs) == 0 || ask.IDs[0] != tc.id) {
			t.Errorf("%q: ids %v, want %s", tc.prompt, ask.IDs, tc.id)
		}
		if tc.harness != "" && ask.Harness != tc.harness {
			t.Errorf("%q: harness %q, want %q", tc.prompt, ask.Harness, tc.harness)
		}
	}
}
