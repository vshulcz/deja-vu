package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// Under -messages the answer is the matching message with its neighbours, of
// this user only, and a snake_case name in the transcript is found by its
// words in the question.
func TestMessagesAnswerWithTheHitAndItsNeighbours(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &server{tables: map[string]map[string]string{}, store: t.TempDir(), token: "k", maxItems: 10, maxChars: 8000, messages: true}
	h := s.routes()
	for i := range 8 {
		add(t, h, fmt.Sprintf("r%d", i), "u1", fmt.Sprintf("noise-%d", i),
			fmt.Sprintf("refactor the billing export number %d", i), "moved the export into a worker")
	}
	add(t, h, "rq", "u1", "sess:quorum",
		"the follower keeps dropping out of the cluster",
		"looked at the logs, the leader sees heartbeats arrive late",
		"which setting decides how long it waits",
		"raised election_timeout_ms to 900 in raft.toml",
		"thanks, that held overnight")
	add(t, h, "ro", "u2", "sess:other", "election timeout on the follower", "other user's fix")

	code, out := post(t, h, "/search", "Bearer k", map[string]any{"query": "which election timeout setting", "user_id": "u1", "top_k": 2})
	data, _ := out["data"].([]any)
	if code != 200 || len(data) == 0 || len(data) > 2 {
		t.Fatalf("search: %d %v", code, out)
	}
	if id := field(t, data[0], "id"); !strings.HasPrefix(id, "sess:quorum#") {
		t.Fatalf("first item is %q, want the quorum session", id)
	}
	c := field(t, data[0], "content")
	// One window, in the session's order: the neighbours read as the
	// conversation they were, not as separate hits.
	want := "heartbeats arrive late\n[user] which setting decides how long it waits\n[assistant] raised election_timeout_ms"
	if !strings.Contains(c, want) {
		t.Fatalf("answer lacks the hit with its neighbours:\n%s", c)
	}
	for _, d := range data {
		if strings.Contains(field(t, d, "content"), "other user's fix") {
			t.Fatalf("u1's search served u2's memory")
		}
	}
}

// However many messages match, the answer stays inside the character budget
// and each message is shortened on a character boundary.
func TestMessagesAnswerStaysInsideTheCut(t *testing.T) {
	for _, def := range []bool{false, true} {
		t.Run(fmt.Sprint("defaults=", def), func(t *testing.T) { answerInsideTheCut(t, def) })
	}
}

func answerInsideTheCut(t *testing.T, def bool) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s := &server{tables: map[string]map[string]string{}, store: t.TempDir(), maxItems: 10, messages: true}
	if def {
		s.defaults()
	}
	h := s.routes()
	long := "шард миграция " + strings.Repeat("ёжик ", 700)
	for i := range 3 {
		msgs := make([]string, 0, 150)
		for range 150 {
			msgs = append(msgs, long)
		}
		add(t, h, fmt.Sprintf("r%d", i), "u", fmt.Sprintf("s%d", i), msgs...)
	}
	_, out := post(t, h, "/search", "", map[string]any{"query": "шард миграция", "user_id": "u", "top_k": 100})
	data, _ := out["data"].([]any)
	total := 0
	for _, d := range data {
		c := field(t, d, "content")
		total += len(c)
		if !utf8.ValidString(c) {
			t.Fatalf("message cut inside a character")
		}
	}
	if len(data) == 0 || len(data) > 100 || total > answerChars {
		t.Fatalf("%d items, %d bytes; cut is %d", len(data), total, answerChars)
	}
	if total < answerChars/2 {
		t.Fatalf("answer of %d bytes leaves most of the cut unused", total)
	}
}

func TestIdentifiersSplitIntoWords(t *testing.T) {
	got := strings.Join(terms("ModelChoiceField separability_matrix HTTP2Server x"), " ")
	want := "modelchoicefield model choice field separability_matrix separability matrix http2server http2 server x"
	if got != want {
		t.Fatalf("terms = %q, want %q", got, want)
	}
}

// -result shows what a past session came to: the library change whole and
// before its tests, without the agent's scratch script, its own notes or a
// draft a later edit replaced.
func TestResultShowsTheFinalChangeFirst(t *testing.T) {
	edit := func(file, before, after string) string {
		b, err := json.Marshal(map[string]string{"file_path": file, "old_string": before, "new_string": after})
		if err != nil {
			t.Fatal(err)
		}
		return "[tool_use Edit] " + string(b)
	}
	write := func(file, content string) string {
		b, err := json.Marshal(map[string]string{"file_path": file, "content": content})
		if err != nil {
			t.Fatal(err)
		}
		return "[tool_use Write] " + string(b)
	}
	turns := []turn{
		{"user", "squashed migrations keep index_together"},
		{"assistant", write("./work/testbed/reproduce_bug.py", "print('scratch')")},
		{"assistant", edit("./work/testbed/django/db/ops.py", "def reduce(self):\n    pass", "def reduce(self):\n    return DRAFT")},
		{"assistant", edit("./work/testbed/django/db/ops.py", "return DRAFT", "return [AddIndex(self.name)]")},
		{"assistant", edit("./work/testbed/tests/test_ops.py", "def test_a(): pass", "def test_a(): assert reduce()")},
		{"assistant", write("./work/NOTES.md", "my notes")},
		{"assistant", "Fixed: CreateModel.reduce now turns RenameIndex into AddIndex."},
	}
	got := result("past-1", turns, strings.Repeat("transcript ", 50), nil, 8000, nil)
	for _, bad := range []string{"scratch", "my notes", "DRAFT"} {
		if strings.Contains(got, bad) {
			t.Fatalf("result shows %q:\n%s", bad, got)
		}
	}
	lib, test := strings.Index(got, "return [AddIndex(self.name)]"), strings.Index(got, "assert reduce()")
	if lib < 0 || test < 0 || lib > test || !strings.Contains(got, "turns RenameIndex into AddIndex") {
		t.Fatalf("library change, its test and the ending must all show, library first:\n%s", got)
	}
	if !strings.Contains(got, "Files: django/db/ops.py, tests/test_ops.py") {
		t.Fatalf("result lacks touched files:\n%s", got)
	}
}

func TestDefaultsPutTheRightSessionFirstAndNameOthers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &server{tables: map[string]map[string]string{}, store: t.TempDir(), maxItems: 10, maxChars: 8000}
	s.defaults()
	h := s.routes()
	add(t, h, "r1", "u", "sess:alpha", "migrating shard routing table", "updated router/shard.go to split ranges")
	add(t, h, "r2", "u", "sess:beta", "shard rebalance worker stalls on empty range", "fixed rebalance loop in router/rebalance.go")
	add(t, h, "r3", "u", "sess:gamma", "shard healthcheck timeout", "lowered probe interval in router/health.go")

	code, out := post(t, h, "/search", "", map[string]any{"query": "shard rebalance empty range", "user_id": "u", "top_k": 10})
	got := ids(t, out)
	if code != 200 || len(got) < 3 {
		t.Fatalf("search: %d %v", code, got)
	}
	if got[0] != "sess:beta" {
		t.Fatalf("first item is %q, want sess:beta", got[0])
	}
	if !strings.HasSuffix(got[1], "#brief") {
		t.Fatalf("second item is %q, want #brief", got[1])
	}
	data, _ := out["data"].([]any)
	brief := field(t, data[1], "content")
	if !strings.Contains(brief, "sess:alpha") || !strings.Contains(brief, "sess:gamma") {
		t.Fatalf("brief lacks other matching sessions:\n%s", brief)
	}
}
