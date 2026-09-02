package index

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// A first build has nothing to answer with until it finishes, and on a large
// corpus that is the wait a user decides in (#505). The newest slice is
// published first, so the wait is for the tail rather than for everything.
func TestAFirstBuildAnswersBeforeItFinishes(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	now := time.Now()
	var ss []model.Session
	for i := 0; i < 500; i++ {
		ss = append(ss, model.Session{
			ID: fmt.Sprintf("s%03d", i), Harness: "claude", Project: "p",
			Updated: now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{{Role: "user",
				Text: fmt.Sprintf("session %d talked about the zephyrine%d rollout", i, i)}},
		})
	}
	publishNewestFirst(dir, ss, io.Discard)

	if !HasManifest(dir) {
		t.Fatal("nothing was published, so the build still answers nothing until it ends")
	}
	// The newest sessions are there and searchable, and it is the newest they
	// are: a slice of the oldest would answer nothing anyone is about to ask.
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != partialPublishSessions {
		t.Errorf("the slice holds %d sessions, want %d", len(metas), partialPublishSessions)
	}
	held := map[string]bool{}
	for _, meta := range metas {
		held[meta.ID] = true
	}
	if !held["s000"] || !held["s199"] {
		t.Errorf("the newest sessions are not the ones published: %v", ids(sessionsOfMetas(metas)))
	}
	if held["s499"] {
		t.Error("the oldest session was published ahead of newer ones")
	}
	res, err := SearchDetailed(dir, query.Options{Query: "zephyrine0", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Error("the newest session is not searchable in the published slice")
	}
	// And it claims no file coverage: the state of the files is what tells the
	// next incremental run what it may skip, and this index holds a slice.
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 0 {
		t.Errorf("the published slice claimed %d files as parsed", len(m.Files))
	}
}

// It publishes only where the wait is real, and never over an index that
// already answers.
func TestTheEarlyPublishHoldsBackWhereItWouldNotHelp(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	now := time.Now()
	small := make([]model.Session, 0, 10)
	for i := 0; i < 10; i++ {
		small = append(small, model.Session{
			ID: fmt.Sprintf("s%d", i), Harness: "claude", Project: "p", Updated: now,
			Messages: []model.Message{{Role: "user", Text: "a short store"}},
		})
	}
	dir := filepath.Join(tmp, "small")
	publishNewestFirst(dir, small, io.Discard)
	if HasManifest(dir) {
		t.Error("a corpus that builds fast anyway paid for a second pass")
	}

	// And where an index already exists, replacing it with a slice would be a
	// regression rather than a head start.
	dir2 := filepath.Join(tmp, "existing")
	var many []model.Session
	for i := 0; i < 500; i++ {
		many = append(many, model.Session{
			ID: fmt.Sprintf("s%03d", i), Harness: "claude", Project: "p",
			Updated:  now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{{Role: "user", Text: fmt.Sprintf("old session %d", i)}},
		})
	}
	if err := os.MkdirAll(filepath.Join(dir2+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir2+".tmp", dir2, many, nil, ""); err != nil {
		t.Fatal(err)
	}
	res, err := SearchDetailed(dir2, query.Options{Query: "old session 499", All: true})
	if err != nil {
		t.Fatal(err)
	}
	before := len(res.Sessions)
	publishNewestFirst(dir2, many, io.Discard)
	res, err = SearchDetailed(dir2, query.Options{Query: "old session 499", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != before {
		t.Errorf("an index that already answered was replaced by a slice: %d sessions, was %d",
			len(res.Sessions), before)
	}
}

// sessionsOfMetas is for the failure message only: what got published.
func sessionsOfMetas(metas []SessionMeta) []model.Session {
	out := make([]model.Session, 0, len(metas))
	for _, meta := range metas {
		out = append(out, model.Session{ID: meta.ID, Harness: meta.Harness})
	}
	return out
}

// And the build itself says so: the line is what tells a person the wait is
// for the tail rather than for everything.
func TestARealFirstBuildSaysItIsSearchableAlready(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := filepath.Join(tmp, "claude")
	now := time.Now()
	for i := 0; i < 420; i++ {
		at := now.Add(-time.Duration(i) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("b%03d", i)
		writeLines(t, filepath.Join(claude, "-proj", id+".jsonl"),
			claudeLine(id, at, fmt.Sprintf("session %d about the rollout", i)))
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "idx")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "searchable now") {
		t.Errorf("a first build over 420 sessions published nothing early:\n%s", out.String())
	}
	// And the finished index holds everything, not the slice.
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 420 {
		t.Errorf("the finished index holds %d sessions, want 420", len(metas))
	}
}
