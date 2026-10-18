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

// A fork carries its source's edit records with the source's timestamps, so
// the record that made a line sits in both at the same instant. Candidates
// arrive newest first and the first one kept the tie, so a Claude /branch, or
// a fork of it, was credited with the edit its source made.
func TestBlameLineCreditsTheSourceNotItsFork(t *testing.T) {
	const line = "package main // WOMBAT-9 parent"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "main.go", Line: 1}
	commit := lineCommit{SHA: "f90b6e0e", When: time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)}
	at := commit.When.Add(-20 * time.Minute)
	replaced := "package main"
	session := func(id, kind, parent string) model.Session {
		return model.Session{Harness: "claude", ID: id, Kind: kind, Parent: parent, Messages: []model.Message{
			{Role: "user", Text: "edit main.go", Time: at.Add(-time.Second)},
			{Role: sources.RoleEdit, Text: path + "\n" + replaced, Time: at},
			{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: at},
		}}
	}
	src := session("6f40857d", "", "")
	branch := session("84f06bb6", "fork", "6f40857d")
	twig := session("46345f36", "fork", "84f06bb6")
	removed := map[string]bool{blameSpanKey(replaced): true}

	orders := [][]model.Session{
		{branch, src},
		{twig, src, branch},
		{twig, branch, src},
		{src, twig, branch},
	}
	for _, ss := range orders {
		for _, rm := range []map[string]bool{removed, nil} {
			got, ok := attributeLine(ss, target, commit, rm)
			if !ok || got.Session.ID != "6f40857d" {
				t.Errorf("order %s, replaced=%v: credited %q, want the source 6f40857d", forkOrder(ss), rm != nil, got.Session.ID)
			}
		}
	}

	// Control: a fork that wrote the line itself, later than the source, keeps it.
	own := session("84f06bb6", "fork", "6f40857d")
	own.Messages[1].Time, own.Messages[2].Time = at.Add(time.Minute), at.Add(time.Minute)
	if got, _ := attributeLine([]model.Session{src, own}, target, commit, removed); got.Session.ID != "84f06bb6" {
		t.Errorf("a fork's own later edit went to %q", got.Session.ID)
	}
	// And two unrelated sessions tied at one instant keep the first, as before.
	other := session("aaaa", "", "")
	if got, _ := attributeLine([]model.Session{other, src}, target, commit, removed); got.Session.ID != "aaaa" {
		t.Errorf("an unrelated tie moved to %q", got.Session.ID)
	}
}

func forkOrder(ss []model.Session) string {
	out := ""
	for _, s := range ss {
		out += s.ID + " "
	}
	return out
}
