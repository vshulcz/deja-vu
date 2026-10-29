package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func post(t *testing.T, h http.Handler, path, auth string, body any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(b))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func add(t *testing.T, h http.Handler, req, user, session string, msgs ...string) {
	t.Helper()
	mm := make([]map[string]any, 0, len(msgs))
	for i, m := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		mm = append(mm, map[string]any{"role": role, "content": m})
	}
	code, out := post(t, h, "/add", "Bearer k", map[string]any{"request_id": req, "user_id": user, "session_id": session, "messages": mm})
	if code != 200 || out["success"] != true || out["request_id"] != req || out["user_id"] != user || out["session_id"] != session {
		t.Fatalf("add %s: %d %v", req, code, out)
	}
}

func ids(t *testing.T, out map[string]any) []string {
	t.Helper()
	data, _ := out["data"].([]any)
	r := make([]string, 0, len(data))
	for _, d := range data {
		r = append(r, field(t, d, "id"))
	}
	return r
}

// field is item[key] for an item of a reply's data array.
func field(t *testing.T, item any, key string) string {
	t.Helper()
	m, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("data item is a %T", item)
	}
	s, ok := m[key].(string)
	if !ok {
		t.Fatalf("data item %s is a %T", key, m[key])
	}
	return s
}

// The contract the leaderboard checks: Add echoes the ids and makes the
// chunk searchable before it answers, Search returns at most top_k items of
// this user_id only, and a retried request_id is not stored twice.
func TestAddThenSearchFollowsTheContract(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &server{tables: map[string]map[string]string{}, store: t.TempDir(), token: "k", maxItems: 10, maxChars: 8000}
	h := s.routes()
	for i := range 12 {
		add(t, h, fmt.Sprintf("r%d", i), "u1", fmt.Sprintf("noise-%d", i),
			fmt.Sprintf("refactor the billing export number %d", i), "moved the export into a worker")
	}
	add(t, h, "rq", "u1", "sess:quorum", "the quorum timeout keeps firing on the raft follower", "raised election_timeout_ms to 900 in raft.toml")
	add(t, h, "rq", "u1", "sess:quorum", "the quorum timeout keeps firing on the raft follower", "raised election_timeout_ms to 900 in raft.toml")
	add(t, h, "ro", "u2", "sess:other", "quorum timeout on the raft follower again", "other user's fix")

	code, out := post(t, h, "/search", "Token k", map[string]any{"query": "raft follower quorum timeout", "user_id": "u1", "top_k": 3})
	got := ids(t, out)
	if code != 200 || len(got) == 0 || got[0] != "sess:quorum" {
		t.Fatalf("search: %d %v", code, out)
	}
	if len(got) > 3 {
		t.Fatalf("returned %d items for top_k 3", len(got))
	}
	for _, id := range got {
		if id == "sess:other" {
			t.Fatalf("u1's search served u2's memory: %v", got)
		}
	}
	data, _ := out["data"].([]any)
	content := field(t, data[0], "content")
	if !strings.Contains(content, "election_timeout_ms") || strings.Count(content, "raft.toml") != 1 {
		t.Fatalf("content lost the answer or stored the retried chunk twice:\n%s", content)
	}

	if _, out := post(t, h, "/search", "Bearer k", map[string]any{"query": "raft quorum", "user_id": "nobody", "top_k": 5}); out["data"] == nil || len(ids(t, out)) != 0 {
		t.Fatalf("unknown user_id must return an empty data array: %v", out)
	}
	if code, _ := post(t, h, "/search", "Bearer wrong", map[string]any{"query": "raft", "user_id": "u1", "top_k": 5}); code != http.StatusUnauthorized {
		t.Fatalf("wrong key answered %d", code)
	}
	if code, _ := post(t, h, "/add", "Bearer k", map[string]any{"request_id": "x", "user_id": "u1", "session_id": "s", "messages": []any{}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("empty messages answered %d", code)
	}
}

// A session longer than the budget is cut on a character, not inside one.
func TestLongSessionIsCutOnARune(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &server{tables: map[string]map[string]string{}, store: t.TempDir(), maxItems: 5}
	h := s.routes()
	add(t, h, "r1", "u", "ru", "миграция индекса падает на шарде "+strings.Repeat("ёжик ", 200), "поправил шард")
	// Cyrillic is two bytes a letter, so one of two neighbouring budgets lands
	// inside a letter whatever the excerpts before it weigh.
	for _, budget := range []int{900, 901} {
		s.maxChars = budget
		_, out := post(t, h, "/search", "", map[string]any{"query": "миграция индекса шард", "user_id": "u", "top_k": 5})
		data, _ := out["data"].([]any)
		if len(data) == 0 {
			t.Fatalf("no result: %v", out)
		}
		c := field(t, data[0], "content")
		if !utf8.ValidString(c) || strings.ContainsRune(c, utf8.RuneError) {
			t.Fatalf("budget %d cut inside a character: %q", budget, c[len(c)-20:])
		}
	}
}
