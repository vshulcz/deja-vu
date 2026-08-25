package index

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// buildWithFiles writes the named transcripts into a fresh store and builds it,
// returning the project directory and the index directory so the caller can
// take files away and build again.
func buildWithFiles(t *testing.T, ids ...string) (proj, dir string) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj = filepath.Join(root, "-tmp-e")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		line := `{"type":"user","sessionId":"` + id + `","cwd":"/tmp/e","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir = filepath.Join(home, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return proj, dir
}

// The third counter of the family: it says how many indexed files went away
// with their store, and the command layer turns that into "the history that was
// here has gone" rather than "nothing to index yet" (#1762). It accumulated
// across builds like the other two (#1861).
func TestTheEvictedCounterBelongsToTheLastBuild(t *testing.T) {
	proj, dir := buildWithFiles(t, "a", "b", "c")

	for _, id := range []string{"a", "b"} {
		if err := os.Remove(filepath.Join(proj, id+".jsonl")); err != nil {
			t.Fatal(err)
		}
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Deliberately not read: the leftover is what the next build must not
	// inherit.
	if err := os.Remove(filepath.Join(proj, "c.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportEvictedFiles(); n != 1 {
		t.Errorf("the second eviction reported %d files, want its own 1", n)
	}
}

// The family itself: every counter drained by a Report function has to be
// cleared where a build starts, or it reports the process rather than the
// build. Three were found one at a time — emptied and collisions in #1850,
// evicted in #1861 — and this is what makes the fourth a build failure.
func TestEveryReportedCounterIsClearedByABuild(t *testing.T) {
	src, err := os.ReadFile("ingest.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	drained := regexp.MustCompile(`func Report\w+\(\) int \{\s*return int\((\w+)\.Swap\(0\)\)`)
	found := drained.FindAllStringSubmatch(body, -1)
	if len(found) < 3 {
		t.Fatalf("expected the counter family, found %d — has the shape changed?", len(found))
	}
	for _, m := range found {
		name := m[1]
		if !strings.Contains(body, name+".Store(0)") {
			t.Errorf("%s is drained by a Report function but never cleared where a build starts: a second build in one process reports the first one's number too", name)
		}
	}
}
