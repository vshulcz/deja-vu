package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

type recordingProgress struct {
	mu       sync.Mutex
	phases   []string
	advances map[string]int
}

func (r *recordingProgress) Phase(name string, _ int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phases = append(r.phases, name)
}

func (r *recordingProgress) Advance(units int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.phases) == 0 {
		return
	}
	if r.advances == nil {
		r.advances = map[string]int{}
	}
	r.advances[r.phases[len(r.phases)-1]] += units
}

func (r *recordingProgress) Harness(string, int, int) {}

// seedClaudeCorpus writes enough of a transcript for a build to have something
// to report on.
func seedClaudeCorpus(t *testing.T, root string) {
	t.Helper()
	proj := filepath.Join(root, "projects", "app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		var b []byte
		when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC).Add(time.Duration(i) * time.Hour)
		for j := 0; j < 4; j++ {
			line, err := json.Marshal(map[string]any{
				"type":      "user",
				"sessionId": "s" + string(rune('a'+i)),
				"timestamp": when.Add(time.Duration(j) * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "connection pool exhausted again"},
			})
			if err != nil {
				t.Fatal(err)
			}
			b = append(append(b, line...), '\n')
		}
		if err := os.WriteFile(filepath.Join(proj, "s"+string(rune('a'+i))+".jsonl"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFirstSearchBuildReportsPhases guards the path almost every user takes to
// their first index: they run a search, not `deja index`. That path built
// through rebuildForSearch, which reported no phases at all, so the first-run
// display sat on "starting" with an unmoving bar for the whole build while
// `deja index` — which few people run first — animated correctly.
func TestFirstSearchBuildReportsPhases(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	seedClaudeCorpus(t, filepath.Join(tmp, "claude"))

	rec := &recordingProgress{}
	SetProgress(rec)
	defer SetProgress(nil)

	dir := filepath.Join(tmp, "idx")
	if err := EnsureForSearch(dir, query.Options{Query: "connection pool"}, false, nil); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, p := range rec.phases {
		seen[p] = true
	}
	for _, want := range []string{"reading sessions", "indexing messages", "writing index"} {
		if !seen[want] {
			t.Fatalf("phase %q never reported; got %v", want, rec.phases)
		}
	}
	// A phase that announces a total and never advances is a bar that does not
	// move, which is the defect this test exists for.
	for _, want := range []string{"indexing messages", "writing index"} {
		if rec.advances[want] == 0 {
			t.Fatalf("phase %q reported no progress; advances=%v", want, rec.advances)
		}
	}
}
