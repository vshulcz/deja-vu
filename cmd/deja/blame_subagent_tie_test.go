package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// goose and Hermes stamp records to the whole second, so a parent and the
// sub-agent it spawned can write one line at the same instant. The tie-break
// meant for forks took the older session and gave the sub-agent's line to
// its parent.
func TestBlameKeepsASubagentsLineOnAWholeSecondTie(t *testing.T) {
	const line = "retries := 5 // NUMBAT-4 written by the delegate"
	dir := t.TempDir()
	path := filepath.Join(dir, "retry.go")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "retry.go", Line: 1}
	commit := lineCommit{SHA: "c0ffee12", When: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)}
	at := commit.When.Add(-10 * time.Minute)
	replaced := "retries := 3"
	for _, harness := range []string{"goose", "hermes"} {
		session := func(id, kind, parent string, started time.Time) model.Session {
			return model.Session{Harness: harness, ID: id, Kind: kind, Parent: parent, Started: started, Messages: []model.Message{
				{Role: "user", Text: "raise the retries", Time: started},
				{Role: sources.RoleEdit, Text: path + "\n" + replaced, Time: at},
				{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: at},
			}}
		}
		parent := session("parent-1", "", "", at.Add(-time.Hour))
		child := session("child-1", "subagent", "parent-1", at.Add(-time.Minute))
		for _, rm := range []map[string]bool{{blameSpanKey(replaced): true}, nil} {
			got, ok := attributeLine([]model.Session{child, parent}, target, commit, rm)
			if !ok || got.Session.ID != "child-1" {
				t.Errorf("%s, replaced=%v: credited %q, want the sub-agent child-1", harness, rm != nil, got.Session.ID)
			}
		}
	}
}
