package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A one-shot transcript recorded in an agent runtime's scratch tree repeats the
// question word for word, which is the strongest lexical match there is, and
// every one of those words is user text carrying the user-role boost. It beat
// the session that had actually settled the question (#2050).
//
// Measured at Run rather than on the live store on purpose: the store has been
// reindexed since and those transcripts have aged out of the top, so only a
// fixture can hold the case still.
func TestAScratchTranscriptDoesNotOutrankTheAnswer(t *testing.T) {
	now := time.Now()
	answered := model.Session{
		ID: "answered", Harness: "claude", Project: "deja-vu",
		Path:    "/Users/x/.claude/projects/deja-vu/answered.jsonl",
		Started: now, Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "who should send the claudeworkshop email", Time: now},
			{Role: "assistant", Text: "You send it yourself — the site has no repository, so there is no pull request to open.", Time: now},
		},
	}
	// The same question, asked of an agent in a job's temp tree and answered
	// with nothing.
	echo := model.Session{
		ID: "echo", Harness: "opencode", Project: "neutral",
		Path:    "/Users/x/.claude/jobs/9f0aa059/tmp/ab2/neutral",
		Started: now, Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "who should send the claudeworkshop email about their outdated article", Time: now},
			{Role: "assistant", Text: "I will look into that.", Time: now},
		},
	}

	hits, err := Run(WithoutScratch([]model.Session{echo, answered}), Options{Query: "who should send the claudeworkshop email", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Session.ID == "echo" {
			t.Errorf("a transcript from a job's scratch tree was returned: %+v", h.Session.Path)
		}
	}
	if len(hits) == 0 || hits[0].Session.ID != "answered" {
		t.Fatalf("the session that answered did not lead: %+v", hits)
	}
}

// The rule has to stay narrower than "anything temporary": people do real work
// in /tmp and in a mktemp -d, and a repository whose name contains "jobs" is
// not a runtime directory.
func TestOrdinaryWorkIsNotMistakenForScratch(t *testing.T) {
	now := time.Now()
	for _, where := range []struct{ name, path, project string }{
		{"a project under /tmp", "/tmp/spike/session.jsonl", "spike"},
		{"a system temp directory", "/var/folders/jn/T/spike/s.jsonl", "spike"},
		{"a repository with jobs in the name", "/Users/x/code/jobs-api/s.jsonl", "jobs-api"},
		{"a project named tmp-something", "/Users/x/code/tmp-migrations/s.jsonl", "tmp-migrations"},
	} {
		t.Run(where.name, func(t *testing.T) {
			s := model.Session{
				ID: "real", Harness: "claude", Project: where.project, Path: where.path,
				Started: now, Updated: now,
				Messages: []model.Message{
					{Role: "user", Text: "the exporter drops rows at utc midnight", Time: now},
					{Role: "assistant", Text: "Root cause was the batch cutoff timezone. We pinned it to UTC.", Time: now},
				},
			}
			hits, err := Run(WithoutScratch([]model.Session{s}), Options{Query: "exporter drops rows midnight", All: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) == 0 {
				t.Error("ordinary work was treated as a runtime's scratch tree")
			}
		})
	}
}
