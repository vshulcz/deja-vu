package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// `blame scripts/load` answered with every session that said "under load":
// the bare name of an extensionless file is an ordinary word. Only the path
// counts for such a name, while `Makefile` keeps matching on its own.
func TestBlameWordNamedFileNeedsItsPath(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{ID: "word", Project: "/repo", Updated: now, Messages: []model.Message{{Role: "user", Text: "the api times out under load, the cache layer returns 500"}}},
		{ID: "path", Project: "/repo", Updated: now, Messages: []model.Message{{Role: "user", Text: "scripts/load now waits for the server before it starts"}}},
	}
	hits := Blame(ss, BlameTarget{FullPath: "/repo/scripts/load", Base: "load", Stem: "load"}, BlameOptions{All: true})
	if len(hits) != 1 || hits[0].Session.ID != "path" {
		t.Fatalf("blame scripts/load = %v, want only the session naming the path", blameIDs(hits))
	}
	mk := []model.Session{{ID: "mk", Project: "/repo", Updated: now, Messages: []model.Message{{Role: "user", Text: "the Makefile test target needs -race"}}}}
	if hits := Blame(mk, BlameTarget{FullPath: "/repo/Makefile", Base: "Makefile", Stem: "Makefile"}, BlameOptions{All: true}); len(hits) != 1 {
		t.Fatalf("blame Makefile = %v, want the bare mention", blameIDs(hits))
	}
}

// A pasted blob in place of a path took 20s to rank; past PATH_MAX it is
// refused before any matching.
func TestBlameRefusesAPathLongerThanAnyFileSystemAllows(t *testing.T) {
	if _, err := ResolveBlamePath(strings.Repeat("a/", 3000) + "x.go"); err == nil {
		t.Fatal("a 6 KB path was accepted")
	}
	if _, err := ResolveBlamePath(strings.Repeat("a/", 100) + "x.go"); err != nil {
		t.Fatalf("a deep but real path was refused: %v", err)
	}
}

func blameIDs(hits []BlameHit) []string {
	var ids []string
	for _, h := range hits {
		ids = append(ids, h.Session.ID)
	}
	return ids
}
