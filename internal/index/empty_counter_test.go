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
