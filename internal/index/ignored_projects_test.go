package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The commands table keys by project while the ignore rule matches on the
// session's path, so the callers that read that table cannot apply the rule
// exactly. This names the projects it touches at all, which is the safe
// direction for them (#2652).
func TestProjectsTouchedByIgnore(t *testing.T) {
	dir := halvedStore(t)
	touched := ProjectsTouchedByIgnore(dir)
	if !touched["w/scratch"] {
		t.Fatalf("the ignored project is not named: %v", touched)
	}
	if touched["w/keep"] {
		t.Fatalf("a project the rule does not touch was named: %v", touched)
	}
	if len(touched) != 1 {
		t.Fatalf("one project is covered by this rule, got %v", touched)
	}
}

// A store with no rule in force names nothing, and a missing store answers
// nothing rather than failing the caller.
func TestProjectsTouchedByIgnoreOnAStoreWithoutOne(t *testing.T) {
	if got := ProjectsTouchedByIgnore(t.TempDir()); len(got) != 0 {
		t.Fatalf("an unreadable store should name no projects, got %v", got)
	}
}

func TestFixesForChecksSessionIgnoreAndTabSeparatedFailLine(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	when := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	writeStore(t, dir, []model.Session{
		{
			ID: "job1", Harness: "claude", Project: "w/keep",
			Path: "/home/u/.claude/jobs/j1/session.jsonl", Updated: when,
			Messages: []model.Message{
				{Role: roleToolOutput, Text: "FAIL\texample.com/pkg/sub [build failed]", Time: when},
				{Role: roleCommand, Text: "go build ./pkg/sub", Time: when.Add(time.Minute)},
			},
		},
		{
			ID: "k1", Harness: "claude", Project: "w/keep",
			Path: "/home/u/.claude/projects/-w-keep/k1.jsonl", Updated: when.Add(2 * time.Minute),
			Messages: []model.Message{
				{Role: roleToolOutput, Text: "FAIL\texample.com/pkg/main [build failed]", Time: when},
				{Role: roleCommand, Text: "go mod tidy && go build ./pkg/main", Time: when.Add(time.Minute)},
			},
		},
		{
			ID: "k2", Harness: "claude", Project: "w/keep",
			Path: "/home/u/.claude/projects/-w-keep/k2.jsonl", Updated: when.Add(3 * time.Minute),
			Messages: []model.Message{
				{Role: roleToolOutput, Text: "FAIL\texample.com/pkg/main [build failed]", Time: when},
				{Role: roleCommand, Text: "go mod tidy && go build ./pkg/main", Time: when.Add(time.Minute)},
			},
		},
	})
	if got := FixesFor(dir, "FAIL example.com/pkg/main [build failed]", 3, nil); len(got) != 1 {
		t.Fatalf("FixesFor on non-ignored session in mixed project = %+v, want 1 fix", got)
	}
	if got := FixesFor(dir, "FAIL example.com/pkg/sub [build failed]", 3, nil); len(got) != 0 {
		t.Fatalf("FixesFor served an ignored job session: %+v", got)
	}
}
