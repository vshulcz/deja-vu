package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// ownCollisionCount makes a test's collision count its own.
//
// The counter is package state that ReportCollisions drains, so a test that
// asserts a number reads whatever an earlier one left behind. Under -shuffle
// that is exactly what happened: "reported 3 collisions on a clean store",
// from a build three tests away.
//
// Drained twice on purpose. Before, so the assertion is about this build; and
// after, so a test that legitimately produces collisions does not hand them to
// whoever runs next — which is how production reads it too, one drain per
// build rather than one before each read.
func ownCollisionCount(t *testing.T) {
	t.Helper()
	ReportCollisions()
	t.Cleanup(func() { ReportCollisions() })
}

// Identity is harness:id and nothing guarantees it is unique: two files named
// session-1.jsonl in different projects produced one manifest row whose project
// came from one transcript and whose title came from the other, differently on
// every build (#698).
func TestSessionsSharingAnIDAreAttributedTheSameWayEveryBuild(t *testing.T) {
	ownCollisionCount(t)
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	for proj, text := range map[string]string{
		"alpha": "the alpha work on pool sizing",
		"beta":  "the beta work on cache eviction",
	} {
		dir := filepath.Join(tmp, "claude", proj)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "session-1.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	var first []SessionMeta
	for run := 0; run < 5; run++ {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		if n := ReportCollisions(); n == 0 {
			t.Errorf("run %d reported no collision", run)
		}
		metas, err := AllMeta(dir)
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			first = metas
			// The file that sorts first owns the row, so the answer does not
			// depend on which one was read first.
			if len(metas) != 1 || metas[0].Project != "alpha" {
				t.Fatalf("metas = %#v", metas)
			}
			continue
		}
		if len(metas) != len(first) || metas[0].Project != first[0].Project || metas[0].Title != first[0].Title {
			t.Fatalf("run %d: %#v, first build had %#v", run, metas, first)
		}
	}
	// Both conversations stay searchable — the collision costs attribution,
	// not content.
	for _, q := range []string{"alpha work pool", "beta work cache"} {
		hits, err := Search(dir, search.Options{Query: q, All: true})
		if err != nil || len(hits) == 0 {
			t.Errorf("%q: %d hits err=%v", q, len(hits), err)
		}
	}
}

func TestReportCollisionsIsZeroWithoutOne(t *testing.T) {
	ownCollisionCount(t)
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "claude", "solo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"a single conversation"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "only.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n != 0 {
		t.Errorf("reported %d collisions on a clean store", n)
	}
}

// The incremental pass walks a map of changed files; unsorted, which session
// won a shared id depended on that order (#698).
func TestIncrementalPassAttributesCollisionsTheSameWay(t *testing.T) {
	ownCollisionCount(t)
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	write := func(proj, text string) {
		dir := filepath.Join(tmp, "claude", proj)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "session-1.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	write("alpha", "the alpha work on pool sizing")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	ReportCollisions()

	// Two more projects arrive, both claiming the same id. Neither may take the
	// row from the transcript that sorts first.
	write("beta", "the beta work on cache eviction")
	write("gamma", "the gamma work on token limits")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n == 0 {
		t.Error("incremental pass reported no collision")
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Project != "alpha" {
		t.Fatalf("metas = %#v", metas)
	}
	// The late arrivals are still searchable: what the collision costs is
	// attribution, not content.
	for _, q := range []string{"beta work cache", "gamma work token"} {
		hits, err := Search(dir, search.Options{Query: q, All: true})
		if err != nil || len(hits) == 0 {
			t.Errorf("%q: %d hits err=%v", q, len(hits), err)
		}
	}
}

// Re-indexing one file of a colliding pair must not drop the other. The
// replacement pass rebuilds the manifest from the files it re-read, so the row
// held by the transcript that did not change has to be carried over — losing it
// was how the first attempt at #698 deleted a session.
func TestReindexingOneHalfOfACollisionKeepsTheOther(t *testing.T) {
	ownCollisionCount(t)
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	write := func(proj, text string) {
		dir := filepath.Join(tmp, "claude", proj)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "session-1.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	write("alpha", "the alpha work on pool sizing")
	write("beta", "the beta work on cache eviction")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	ReportCollisions()

	// Rewrite beta from scratch — shorter, so it takes the replacement path
	// rather than the append one.
	write("beta", "beta rewritten")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Project != "alpha" || metas[0].Title != "the alpha work on pool sizing" {
		t.Fatalf("metas = %#v — alpha's row was lost or overwritten", metas)
	}
	// #699 was the other half of the same collision: records are keyed by
	// harness:id too, so re-indexing beta dropped alpha's text from the postings
	// even though alpha's row survived — content loss until a full rebuild.
	// Alpha was never re-read, so its content must still be there.
	alpha, err := Search(dir, search.Options{Query: "pool sizing", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) == 0 {
		t.Fatal("re-indexing beta dropped alpha's content from the index (#699)")
	}
	beta, err := Search(dir, search.Options{Query: "beta rewritten", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(beta) == 0 {
		t.Fatal("beta's rewritten content was not indexed")
	}
}
