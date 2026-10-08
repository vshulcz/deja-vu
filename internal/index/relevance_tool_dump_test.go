package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// toolDumpIndex lays down the store from #4780: one session that is megabytes
// of JSON tool output, every dump holding most words of any question, beside
// short sessions where someone says the answer.
func toolDumpIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	now := time.Now()
	huge := model.Session{ID: "huge", Harness: "opencode", Project: "home", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "check what the swarm plugin stored for its agents"},
			{Role: "assistant", Text: "reading the plugin storage now"},
		}}
	words := []string{"pgx", "upgrade", "pr", "reviewer", "owner", "review", "export", "logger",
		"structured", "logging", "command", "payouts", "service", "naming", "setup", "main.go"}
	// Two thousand dumps, one in eight carrying the question's words.
	for i := range 2000 {
		var b strings.Builder
		b.WriteString("[")
		for j := range 40 {
			w := fmt.Sprintf("module%d", j)
			if i%8 == 0 {
				w = words[(i+j)%len(words)]
			}
			fmt.Fprintf(&b, `{"type": "tool", "tool": "read", "callID": "call_%d_%d", "state": {"status": "completed", "input": {"filePath": "/u/src/%s/part%d.go"}, "title": "%s"}},`, i, j, w, j, w)
		}
		b.WriteString("{}]")
		huge.Messages = append(huge.Messages, model.Message{Role: roleToolOutput, Text: b.String()})
	}
	sessions := []model.Session{
		huge,
		{ID: "answer", Harness: "claude", Project: "ledgerd", Updated: now.Add(-48 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "who should review the pgx upgrade PR?"},
				{Role: "assistant", Text: "Dana is the owner of pgx, so she is the reviewer for the upgrade."},
			}},
		{ID: "logger", Harness: "claude", Project: "ledgerd", Updated: now.Add(-72 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "the export command should use the structured logger"},
				{Role: "assistant", Text: "cmd/export now logs through the structured logging setup."},
			}},
	}
	for i := range 30 {
		sessions = append(sessions, model.Session{ID: fmt.Sprintf("other%d", i), Harness: "claude", Project: "misc",
			Updated: now.Add(-time.Duration(100+i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("tune the cache eviction for shard %d", i)},
				{Role: "assistant", Text: "lowered the ttl and the hit rate held"},
			}})
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A match inside tool output is not someone saying it. The session that read
// megabytes of JSON took the top place for questions it never discussed,
// above the two-turn session that answers them (#4780).
func TestAToolOutputDumpDoesNotOutrankTheAnswer(t *testing.T) {
	dir := toolDumpIndex(t)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]string{
		"pgx upgrade PR reviewer owner review":                "answer",
		"cmd/export structured logging logger export command": "logger",
	} {
		result, err := relevanceSearch(dir, m, query.Options{Query: q})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) == 0 {
			t.Fatalf("%q: nothing ranked", q)
		}
		if got := result.Sessions[0].ID; got != want {
			t.Errorf("%q: first is %s, want %s", q, got, want)
		}
	}
}
