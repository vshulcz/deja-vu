package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modes are the answers amlserve can be started with; the contract holds for
// each of them.
var modes = map[string]func(*server){
	"raw":      func(*server) {},
	"fields":   func(s *server) { s.fields = true },
	"result":   func(s *server) { s.result = true },
	"messages": func(s *server) { s.messages = true },
	"defaults": func(s *server) { s.defaults() },
}

// The leaderboard sends a long session in chunks of 20 messages, may retry
// any Add with the same request_id and body, and checks every Search for
// user isolation, top_k and the empty answer.
func TestContract(t *testing.T) {
	for name, mode := range modes {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			s := &server{tables: map[string]map[string]string{}, store: t.TempDir(), token: "k", maxItems: 10, maxChars: 8000}
			mode(s)
			h := s.routes()
			for i := range 12 {
				add(t, h, fmt.Sprintf("n%d", i), "u1", fmt.Sprintf("noise-%d", i),
					fmt.Sprintf("refactor the billing export number %d", i), "moved the export into a worker")
			}
			// One session in three chunks, the second sent twice.
			add(t, h, "c1", "u1", "sess:raft", "step one: the raft follower drops out of the cluster", "step two: leader heartbeats arrive late")
			add(t, h, "c2", "u1", "sess:raft", "step three: which raft setting sets the wait", "step four: raised election_timeout_ms to 900 in raft.toml")
			add(t, h, "c2", "u1", "sess:raft", "step three: which raft setting sets the wait", "step four: raised election_timeout_ms to 900 in raft.toml")
			add(t, h, "c3", "u1", "sess:raft", "step five: raft follower held overnight", "step six: closed")
			add(t, h, "o1", "u2", "sess:other", "the raft follower drops out again", "u2 private fix in raft.toml")

			// Stored once each, in the order sent.
			root, proj, _, ok, err := s.userDir("u1", false)
			if err != nil || !ok {
				t.Fatalf("u1 store: %v %v", ok, err)
			}
			sid, _, _ := s.nameOf(filepath.Join(root, "sessions.json"), "s", "sess:raft", false)
			data, err := os.ReadFile(filepath.Join(proj, sid+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			turns := readTurns(data)
			got := make([]string, 0, len(turns))
			for _, tr := range turns {
				got = append(got, strings.SplitN(tr.text, ":", 2)[0])
			}
			if want := "step one,step two,step three,step four,step five,step six"; strings.Join(got, ",") != want {
				t.Fatalf("stored turns %q, want %q", strings.Join(got, ","), want)
			}

			code, out := post(t, h, "/search", "Bearer k", map[string]any{"query": "raft follower election timeout", "user_id": "u1", "top_k": 2})
			items, _ := out["data"].([]any)
			if code != 200 || len(items) == 0 || len(items) > 2 {
				t.Fatalf("top_k 2 gave %d: %d items %v", code, len(items), out)
			}
			total := 0
			var all strings.Builder
			for _, it := range items {
				c := field(t, it, "content")
				if field(t, it, "id") == "" || c == "" {
					t.Fatalf("empty id or content: %v", it)
				}
				total += len(c)
				all.WriteString(c)
			}
			_, wide := post(t, h, "/search", "Bearer k", map[string]any{"query": "raft follower billing export worker", "user_id": "u1", "top_k": 100})
			seen := map[string]bool{}
			prevScore := 2.0
			wideItems, _ := wide["data"].([]any)
			for _, raw := range wideItems {
				id := field(t, raw, "id")
				if seen[id] {
					t.Fatalf("id %q comes twice in one answer", id)
				}
				seen[id] = true
				m, _ := raw.(map[string]any)
				sc, ok := m["score"].(float64)
				if !ok || sc <= 0 || sc > prevScore {
					t.Fatalf("score %v after %v is not positive decreasing", m["score"], prevScore)
				}
				prevScore = sc
			}
			// ContentPart[] array format from memory-api-v1.1 works on both Add and Search.
			cpCode, _ := post(t, h, "/add", "Bearer k", map[string]any{
				"request_id": "cp1", "user_id": "u1", "session_id": "sess:parts",
				"messages": []any{
					map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": " raft follower parts query "}}},
					map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "fixed parts"}}},
				},
			})
			if cpCode != 200 {
				t.Fatalf("ContentPart[] add returned %d", cpCode)
			}
			cpSearchCode, cpOut := post(t, h, "/search", "Bearer k", map[string]any{
				"query":   []any{map[string]any{"type": "text", "text": "raft follower parts query"}},
				"user_id": "u1", "top_k": 5,
			})
			if cpSearchCode != 200 || len(ids(t, cpOut)) == 0 {
				t.Fatalf("ContentPart[] search returned %d %v", cpSearchCode, cpOut)
			}
			if strings.Contains(all.String(), "u2 private fix") {
				t.Fatalf("u1's search served u2's memory")
			}
			if !strings.Contains(all.String(), "election_timeout_ms") {
				t.Fatalf("the answer lost the fix:\n%s", all.String())
			}
			if total > answerChars {
				t.Fatalf("answer of %d chars is past the cut %d", total, answerChars)
			}

			for _, q := range []map[string]any{
				{"query": "kubernetes ingress certificate", "user_id": "u1", "top_k": 100},
				{"query": "raft follower", "user_id": "nobody", "top_k": 100},
			} {
				code, out := post(t, h, "/search", "Bearer k", q)
				items, ok := out["data"].([]any)
				if code != 200 || !ok || len(items) != 0 {
					t.Fatalf("%v: want data [], got %d %v", q, code, out)
				}
			}
		})
	}
}
