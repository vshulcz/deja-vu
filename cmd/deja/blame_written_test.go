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

// A commit that only adds lines — 71% of them — leaves the replaced-span rule
// with nothing to match, and that silence is what #3773 measured: 7.3% of
// committed lines answered. The written side answers the same question from the
// other direction.
func TestALineNobodyReplacedIsAttributedToTheSessionThatWroteIt(t *testing.T) {
	const line = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.go")
	if err := os.WriteFile(path, []byte("package pool\n\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "pool.go", Line: 3}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}

	wrote := func(id string, at time.Time, written string) model.Session {
		return model.Session{
			Harness: "claude", ID: id,
			Messages: []model.Message{{
				Role: sources.RoleWrote,
				Text: sources.WroteRecord(path, written),
				Time: at,
			}},
		}
	}

	// Nothing was deleted, so the strong rule has nothing to say.
	author, found := attributeLine([]model.Session{
		wrote("s1", commit.When.Add(-2*time.Hour), line),
	}, target, commit, nil)
	if !found {
		t.Fatal("a line a session wrote and a commit added is attributed to nobody")
	}
	if author.Session.ID != "s1" || !author.Wrote {
		t.Fatalf("attributed to %q, wrote=%v", author.Session.ID, author.Wrote)
	}

	// The strong rule still wins where it has evidence: replacing the text a
	// commit deleted proves the session made that very change.
	replaced := "cfg.MaxConns = 10 // the number from before this change"
	strong := model.Session{Harness: "claude", ID: "s2", Messages: []model.Message{{
		Role: sources.RoleEdit,
		Text: path + "\n" + replaced,
		Time: commit.When.Add(-time.Hour),
	}}}
	author, found = attributeLine([]model.Session{
		wrote("s1", commit.When.Add(-2*time.Hour), line), strong,
	}, target, commit, map[string]bool{blameSpanKey(replaced): true})
	if !found || author.Session.ID != "s2" || author.Wrote {
		t.Fatalf("the weaker rule won: %q wrote=%v", author.Session.ID, author.Wrote)
	}
}

func TestTheWrittenRuleTakesTheLastSessionBeforeTheCommit(t *testing.T) {
	const line = "pool, err := pgxpool.NewWithConfig(ctx, cfg) // one pool per process"
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.go")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "pool.go", Line: 1}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}

	at := func(id string, d time.Duration) model.Session {
		return model.Session{Harness: "claude", ID: id, Messages: []model.Message{{
			Role: sources.RoleWrote,
			Text: sources.WroteRecord(path, line),
			Time: commit.When.Add(d),
		}}}
	}
	// Three sessions wrote the same line; the commit carried the last one
	// before it, and the one after it wrote something else.
	author, found := attributeLine([]model.Session{
		at("older", -48*time.Hour), at("newer", -time.Hour), at("after", time.Hour),
	}, target, commit, nil)
	if !found {
		t.Fatal("nothing was attributed")
	}
	if author.Session.ID != "newer" {
		t.Errorf("attributed to %q, want the last write before the commit", author.Session.ID)
	}
}

func TestTheWrittenRuleNeedsTheSameFile(t *testing.T) {
	const line = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.go")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "pool.go", Line: 1}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}

	// The same line, written into a different file: a boilerplate line moves
	// between files and the rule has to stay about this one.
	elsewhere := model.Session{Harness: "claude", ID: "elsewhere", Messages: []model.Message{{
		Role: sources.RoleWrote,
		Text: sources.WroteRecord(filepath.Join(dir, "other", "cache.go"), line),
		Time: commit.When.Add(-time.Hour),
	}}}
	if _, found := attributeLine([]model.Session{elsewhere}, target, commit, nil); found {
		t.Error("a line written into another file was attributed to this one")
	}
}

func TestAShortLineIsNeverAttributedByTheWrittenRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.go")
	// Below the 24-rune floor: every session in the store wrote this.
	if err := os.WriteFile(path, []byte("\treturn nil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "pool.go", Line: 1}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	s := model.Session{Harness: "claude", ID: "s1", Messages: []model.Message{{
		Role: sources.RoleWrote,
		// The record holds a real line, so the session is a candidate; the
		// question is whether the short line asked about matches anything.
		Text: sources.WroteRecord(path, "\treturn nil\nsomething long enough to be evidence here"),
		Time: commit.When.Add(-time.Hour),
	}}}
	if _, found := attributeLine([]model.Session{s}, target, commit, nil); found {
		t.Error("`return nil` was attributed to a session")
	}
}
