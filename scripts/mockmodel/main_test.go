package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(t *testing.T, s *server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	s.post(w, r)
	return w
}

// The stream terminator is a literal. Marshalling it like every other event
// wrote `data: "[DONE]"`, and a real harness stopped on it with
// `Type validation failed: Value: "[DONE]"` — the mock's own bug, found by
// pointing an actual client at it.
func TestChatStreamEndsWithALiteralDone(t *testing.T) {
	s := &server{reply: "47"}
	w := post(t, s, "/v1/chat/completions", `{"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	body := w.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("no terminator in the stream:\n%s", body)
	}
	if strings.Contains(body, `data: "[DONE]"`) {
		t.Fatalf("the terminator is quoted:\n%s", body)
	}
	if !strings.Contains(body, `"content":"47"`) {
		t.Fatalf("the reply never arrived:\n%s", body)
	}
}

// Being stricter than a permissive endpoint is the point: this is the rule
// deja's opencode plugin broke, and a mock that accepted it would have proved
// the bug absent.
func TestSystemAfterUserIsRejected(t *testing.T) {
	s := &server{reply: "47"}
	w := post(t, s, "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"hi"},{"role":"system","content":"late"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "System message must be at the beginning") {
		t.Fatalf("wrong error: %s", w.Body.String())
	}

	// System first is the shape deja was fixed to produce.
	ok := post(t, s, "/v1/chat/completions",
		`{"messages":[{"role":"system","content":"recall"},{"role":"user","content":"hi"}]}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("system-first was rejected: %d %s", ok.Code, ok.Body.String())
	}
}

// Each protocol has to answer in its own shape, or the harness that speaks it
// cannot be swept at all — codex speaks only the responses API, Claude Code
// only the Anthropic one.
func TestEveryProtocolAnswers(t *testing.T) {
	s := &server{reply: "47"}
	for _, tc := range []struct{ path, body, want string }{
		{"/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`, "chat.completion"},
		{"/v1/responses", `{"input":"hi"}`, "output_text"},
		{"/v1/messages", `{"messages":[{"role":"user","content":"hi"}],"system":"recall"}`, "end_turn"},
	} {
		w := post(t, s, tc.path, tc.body)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", tc.path, w.Code)
			continue
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: answer does not look like its protocol:\n%s", tc.path, w.Body.String())
		}
		var v any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Errorf("%s: invalid JSON: %v", tc.path, err)
		}
	}
}
