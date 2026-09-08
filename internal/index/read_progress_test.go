package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// perFileProgress keeps what a build reported, so a test can ask when the
// bar moved rather than only where it ended.
type perFileProgress struct {
	mu     sync.Mutex
	phase  string
	steps  map[string][]int // phase -> the advances it received
	totals map[string]int
}

func newPerFileProgress() *perFileProgress {
	return &perFileProgress{steps: map[string][]int{}, totals: map[string]int{}}
}

func (r *perFileProgress) Phase(name string, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = name
	r.totals[name] = total
}

func (r *perFileProgress) Advance(units int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps[r.phase] = append(r.steps[r.phase], units)
}

func (r *perFileProgress) Harness(string, int, int) {}

func (r *perFileProgress) advances(phase string) []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.steps[phase]...)
}

// The read phase advanced once per store, so a machine whose corpus is mostly
// one harness watched "reading sessions 0%" until that harness finished — ten
// seconds of a thirty-second rebuild on a real store (#3372). It moves per
// file now, and a store still counts its weight exactly once.
func TestTheReadPhaseMovesPerFile(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	at := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	const files = 6
	for i := 0; i < files; i++ {
		dir := filepath.Join(claude, "proj")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%d","timestamp":%q,`+
			`"message":{"role":"user","content":"the quokkabloom exporter drops spans"}}`, i, at)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("s%d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	rec := newPerFileProgress()
	SetProgress(rec)
	defer SetProgress(nil)
	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}

	got := rec.advances("reading sessions")
	if len(got) < files {
		t.Errorf("the read phase reported %d advances for %d files: %v", len(got), files, got)
	}
	sum := 0
	for _, n := range got {
		sum += n
	}
	if total := rec.totals["reading sessions"]; sum != total {
		t.Errorf("the read phase advanced %d of %d units — a store counted twice or not at all", sum, total)
	}
}
