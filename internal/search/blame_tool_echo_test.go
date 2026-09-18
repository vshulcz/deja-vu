package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A tool's own sentence about the file is not why the file looks the way it
// does. Measured over 1,439 paths on a real store, 1,567 of the 7,417 quoted
// snippets were one — a write confirmation, a patch receipt — and 540 rows had
// nothing else to show (#3721).
func TestBlameDoesNotQuoteAToolsWriteConfirmation(t *testing.T) {
	now := time.Now().UTC()
	target := BlameTarget{FullPath: "/work/app/internal/pool.go", Base: "pool.go", Stem: "pool"}
	s := model.Session{
		Harness: "claude", ID: "s1", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: "assistant", Text: "internal/pool.go keeps one connection per shard because the driver's own pool is per process", Time: now},
			{Role: sources.RoleToolOutput, Text: "The file /work/app/internal/pool.go has been updated successfully. (file state is current in your context — no need to Read it back)", Time: now.Add(time.Second)},
			{Role: sources.RoleToolOutput, Text: "File created successfully at: /work/app/internal/pool.go", Time: now.Add(2 * time.Second)},
		},
	}
	hits := Blame([]model.Session{s}, target, BlameOptions{All: true})
	if len(hits) != 1 {
		t.Fatalf("want the one session, got %d", len(hits))
	}
	joined := strings.Join(hits[0].Snippets, " | ")
	for _, echo := range []string{"has been updated successfully", "File created successfully"} {
		if strings.Contains(joined, echo) {
			t.Errorf("quoted a tool's own sentence as the reason: %q", joined)
		}
	}
	if !strings.Contains(joined, "one connection per shard") {
		t.Errorf("the sentence that says why is missing: %q", joined)
	}

	// A command the session ran is evidence, not echo — and a command record
	// starts with "$ ", which the artifact predicate treats as an artifact.
	cmd := model.Session{
		Harness: "claude", ID: "s2", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "$ go test ./internal/pool.go", Time: now},
		},
	}
	got := Blame([]model.Session{cmd}, target, BlameOptions{All: true})
	if len(got) != 1 || len(got[0].Snippets) == 0 {
		t.Fatalf("a command that names the file is evidence: %+v", got)
	}
}
