package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The install used to end with a placeholder — `deja "something you fixed weeks
// ago"` — which leaves the newcomer to invent a question at the moment they know
// least about what is in there. The prompt it prints instead is built from their
// own history and run before it is shown, because a prompt that misses on the
// first try is worse than none (#575).
func TestTheInstallPromptComesFromHistoryAndHasBeenRun(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-work-api", "a.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"pgbouncer prepared statements keep failing under the pool"}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we decided to run the pooler in transaction mode and turn the driver cache off"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "-work-api", "b.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-07-14T09:00:00Z","message":{"role":"user","content":"pgbouncer prepared statements again after the driver upgrade"}}`,
		`{"type":"assistant","sessionId":"s2","timestamp":"2026-07-14T09:20:00Z","message":{"role":"assistant","content":[{"type":"text","text":"the fix was to keep transaction mode and leave the driver cache off"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	p, ok := buildTryPrompt(dir)
	if !ok {
		t.Fatal("no prompt was built from a history that holds the same topic twice, with a decision")
	}
	if !strings.Contains(p.Question, "pgbouncer") {
		t.Errorf("question = %q, want the topic the sessions share", p.Question)
	}
	if !strings.HasPrefix(p.Question, "what did we decide about ") || !strings.HasSuffix(p.Question, "?") {
		t.Errorf("question = %q, want a question", p.Question)
	}
	if p.Sessions < 2 {
		t.Errorf("sessions = %d, want the several the topic appears in", p.Sessions)
	}

	var out bytes.Buffer
	printTryPrompt(&out, p)
	got := out.String()
	if !strings.Contains(got, "paste this into your agent") || !strings.Contains(got, p.Question) {
		t.Errorf("the line is not pasteable:\n%s", got)
	}
	// The provenance is the part that says this is their own work rather than
	// an example out of a README.
	if !strings.Contains(got, "picked from your history") || !strings.Contains(got, "Jun 2026 – Jul 2026") {
		t.Errorf("the provenance line is wrong:\n%s", got)
	}
}

// A store with nothing to build a prompt from must not produce one: the
// placeholder is the honest fallback. Each of these is a gate that was missing
// at some point while this was written.
func TestNoPromptWithoutSeveralSessionsAndADecision(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string][]string
	}{
		{
			// A topic nothing decided answers the question with the question.
			name: "one session, no decision",
			files: map[string][]string{"a.jsonl": {
				`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"pgbouncer prepared statements keep failing under the pool"}}`,
				`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"looking at the pool configuration now"}]}}`,
			}},
		},
		{
			// One session that settled something is a fact about one session.
			// The prompt claims the topic runs through their history, so two is
			// the floor — otherwise the agent answers from the single session
			// the reader happens to be looking at.
			name: "a decision, but only one session on the topic",
			files: map[string][]string{"a.jsonl": {
				`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"quaxbolt overflow in the ledger importer"}}`,
				`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we decided to cast the quaxbolt counter to int64 before the ledger sum"}]}}`,
			}},
		},
		{
			// Two sessions on one topic and nothing settled in either: the
			// question says "what did we decide", so this would answer with the
			// question again.
			name: "several sessions, no decision in any of them",
			files: map[string][]string{
				"a.jsonl": {
					`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"quaxbolt overflow in the ledger importer"}}`,
					`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"reading the importer now to see where the counter comes from"}]}}`,
				},
				"b.jsonl": {
					`{"type":"user","sessionId":"s2","timestamp":"2026-07-02T12:00:00Z","message":{"role":"user","content":"quaxbolt overflow again in the ledger importer"}}`,
					`{"type":"assistant","sessionId":"s2","timestamp":"2026-07-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"still reading; the counter is summed in two places"}]}}`,
				},
			},
		},
		{
			// Two sessions, a decision in both, and the first line of each is a
			// pasted command: a topic lifted out of one reads as noise, which is
			// what the first candidate on a real store was.
			name: "the topic would come from a pasted command",
			files: map[string][]string{
				"a.jsonl": {
					`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"ssh -o ConnectTimeout=25 -p 2222 deploy@example.com restart the importer"}}`,
					`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we decided to restart the importer from the deploy host"}]}}`,
				},
				"b.jsonl": {
					`{"type":"user","sessionId":"s2","timestamp":"2026-07-02T12:00:00Z","message":{"role":"user","content":"ssh -o ConnectTimeout=25 -p 2222 deploy@example.com restart the importer again"}}`,
					`{"type":"assistant","sessionId":"s2","timestamp":"2026-07-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"the fix was to restart the importer from the deploy host again"}]}}`,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			root := os.Getenv("DEJA_CLAUDE_ROOT")
			for name, lines := range tc.files {
				writeClaudeFixture(t, filepath.Join(root, "-work-api", name), name, lines)
			}
			dir := index.DefaultDir()
			if err := index.Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			if p, ok := buildTryPrompt(dir); ok {
				t.Errorf("a prompt was built anyway: %q over %d sessions", p.Question, p.Sessions)
			}
		})
	}
}

// What counts as a topic, and what does not. The rejections are all shapes a
// real store handed over: a pasted ssh command as the first line of a session,
// a path, a bare number.
func TestATopicIsWhatSomebodyWouldSayOutLoud(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		want  string
	}{
		{"content words in the order written", "tighten the pgbouncer prepared statement pool", "tighten pgbouncer prepared statement pool"},
		{"stop words and short words go", "why is the ci job so slow now", "job slow now"},
		{"a path is not a topic", "fix internal/index/sync.go watermark handling", "fix watermark handling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(promptTopicWords(tc.title), " "); got != tc.want {
				t.Errorf("topic = %q, want %q", got, tc.want)
			}
		})
	}
	if got := promptTopicWords("deploy"); got != nil {
		t.Errorf("one word is not a topic: %q", got)
	}
	for _, pasted := range []string{
		"ssh -o ConnectTimeout=25 -p 2222 root@example.com and restart the box",
		"https://example.com/thread/12 says the opposite",
		"cat log | grep timeout",
	} {
		if !pastedHeadline(pasted) {
			t.Errorf("a pasted line was taken for a topic: %q", pasted)
		}
	}
	if pastedHeadline("tighten the pgbouncer prepared statement pool") {
		t.Error("a sentence someone typed was taken for a paste")
	}
}

func TestThePromptSpanReadsAsOneMonthWhenItIsOne(t *testing.T) {
	jun := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	if got := promptSpan(jun, jun.Add(48*time.Hour)); got != "Jun 2026" {
		t.Errorf("span = %q, want one month", got)
	}
	if got := promptSpan(time.Time{}, jun); got != "" {
		t.Errorf("span = %q, want nothing when a date is missing", got)
	}
}
