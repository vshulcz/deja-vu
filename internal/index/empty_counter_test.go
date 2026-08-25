package index

import (
	"os"
	"path/filepath"
	"testing"
)

// buildWithEmpties writes one real transcript and n empty ones into a fresh
// store, builds it, and returns the index directory.
func buildWithEmpties(t *testing.T, empties int) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(root, "-tmp-g")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	real := `{"type":"user","sessionId":"real","cwd":"/tmp/g","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "real.jsonl"), []byte(real), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range empties {
		id := string(rune('a' + i))
		body := `{"type":"user","sessionId":"empty` + id + `","cwd":"/tmp/g","timestamp":"2026-08-01T08:00:00Z","message":{"role":"user","content":"   "}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, "empty"+id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The counter says "since the last build" and counted since the last read, so a
// second build in one process reported its own empty transcripts plus whatever
// an earlier build had left behind (#1850). One process is one build for the
// CLI, which is why it never showed there — the test binary and the server's
// warmup are where several builds share a process.
func TestTheEmptyCounterBelongsToTheLastBuild(t *testing.T) {
	buildWithEmpties(t, 2)
	// Deliberately not read: the leftover is what the next build must not
	// inherit.
	buildWithEmpties(t, 1)
	if n := ReportEmptySessions(); n != 1 {
		t.Errorf("the second build reported %d empty transcripts, want its own 1", n)
	}
	// And a build with none reports none, rather than the previous build's.
	buildWithEmpties(t, 0)
	if n := ReportEmptySessions(); n != 0 {
		t.Errorf("a build with nothing empty reported %d", n)
	}
}

// The collision counter sits beside the empty one and had the same flaw: a
// second build in one process reported its own colliding ids plus the ones an
// earlier build had counted (#1850).
func TestTheCollisionCounterBelongsToTheLastBuild(t *testing.T) {
	build := func(pairs int) {
		home := t.TempDir()
		setHome(t, home)
		t.Setenv("USERPROFILE", home)
		root := filepath.Join(home, "claude")
		t.Setenv("DEJA_CLAUDE_ROOT", root)
		proj := filepath.Join(root, "-tmp-c")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range pairs {
			id := "dup" + string(rune('a'+i))
			line := `{"type":"user","sessionId":"` + id + `","cwd":"/tmp/c","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}` + "\n"
			// One id in two files is what makes a collision.
			for _, half := range []string{"-one.jsonl", "-two.jsonl"} {
				if err := os.WriteFile(filepath.Join(proj, id+half), []byte(line), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := Ensure(filepath.Join(home, "idx"), "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	build(2)
	// Deliberately not read.
	build(1)
	if n := ReportCollisions(); n != 1 {
		t.Errorf("the second build reported %d collisions, want its own 1", n)
	}
	build(0)
	if n := ReportCollisions(); n != 0 {
		t.Errorf("a build with no collisions reported %d", n)
	}
}

// An incremental pass is a build too. Two of them with nobody reading in
// between used to report the first one's colliding ids alongside the second's
// (#1850) — the reviewer of #1851 found this path after the two full ones were
// fixed.
func TestAnIncrementalPassCountsOnlyItsOwn(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(root, "-tmp-c")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(id string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/tmp/c","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}` + "\n"
	}
	pair := func(id string) {
		t.Helper()
		for _, half := range []string{"-one.jsonl", "-two.jsonl"} {
			if err := os.WriteFile(filepath.Join(proj, id+half), []byte(line(id)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := filepath.Join(home, "idx")

	pair("dupa")
	pair("dupb")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n != 2 {
		t.Fatalf("the full build found %d colliding ids, want the 2 it was given", n)
	}

	// Two incremental passes, and nothing reads between them.
	pair("dupc")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pair("dupd")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n != 1 {
		t.Errorf("the last incremental reported %d colliding ids, want its own 1", n)
	}
}
