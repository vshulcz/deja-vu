package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// `passParsed`, `passRead` and `passWholeStores` are package state, cleared by
// `beginPass` and read by the fold, on the grounds that a pass holds the
// directory lock and only one is ever in flight. Two passes in one process is
// the case that grounds rests on — the MCP server can start one while a hook
// warmup starts another — and nothing exercised it: the swap tests read during
// a rebuild, they do not run two.
//
// The reader half is here too, and asks for more than the swap test does: not
// only that a search avoids naming an internal path, but that it keeps
// answering. A rebuild that empties the answer for a moment is a search that
// says "no matches" about a store that has them.
func TestTwoPassesAtOnceKeepTheCountsAndTheAnswers(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	const sessions = 40
	for i := 0; i < sessions; i++ {
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%02d","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"turn %d: the pool timed out during the migration"}}`, i, stamp, i)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%02d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	// A store that already exists, so the readers are asking a real index
	// rather than a missing one.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 4)
	// Both passes have work to do: a rebuild reads everything, and the file
	// written here is what makes the incremental one more than an early
	// return. Without it the second call finds the manifest fresh and never
	// takes the lock at all.
	newLine := fmt.Sprintf(`{"type":"user","sessionId":"s%02d","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"one more turn about the pool and the migration"}}`, sessions, stamp)
	if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%02d.jsonl", sessions)), []byte(newLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// All four start together, and the windows the two passes occupy are kept,
	// so the test can say whether they really contended: without a barrier the
	// scheduler can run one pass to completion before the other begins, and
	// the overlap this is about would be a matter of luck.
	start := make(chan struct{})
	var mu sync.Mutex
	starts := make([]time.Time, 2)
	ends := make([]time.Time, 2)
	passesDone := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			switch i {
			case 0, 1:
				// One rebuild and one incremental pass, which is the pair a
				// warmup and a tool call make.
				mu.Lock()
				starts[i] = time.Now()
				mu.Unlock()
				err := Ensure(dir, "", i == 0, nil)
				mu.Lock()
				ends[i], errs[i] = time.Now(), err
				mu.Unlock()
			default:
				// Until the passes are finished, not a fixed number of rounds:
				// forty sessions rebuild in milliseconds, and a loop that
				// counts can be over before the pass begins.
				// At least one round happens whatever the scheduler does, so a
				// reader that lands late fails nothing; what it must never do
				// is come back empty or corrupt.
				rounds := 0
				for done := false; !done; {
					select {
					case <-passesDone:
						done = true
					default:
					}
					// The door every surface uses. The plain one fails here
					// about one round in eighty — a reader that lands between
					// a pass's append and its manifest write is told the store
					// is crash-truncated (#2176) — which is what the recovery
					// wrapper is for, and what `deja files` was missing.
					hits, err := SearchWithRecovery(dir, search.Options{Query: "pool migration", All: true}, nil)
					rounds++
					if err != nil {
						errs[i] = fmt.Errorf("round %d: %w", rounds, err)
						return
					}
					if len(hits) == 0 {
						errs[i] = fmt.Errorf("round %d: the search found nothing while a pass ran", rounds)
						return
					}
				}
			}
		}(i)
	}
	close(start)
	// The readers stop once both passes are back.
	go func() {
		for i := 0; i < 2; i++ {
			for {
				mu.Lock()
				done := !ends[i].IsZero()
				mu.Unlock()
				if done {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		close(passesDone)
	}()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	// The two calls really did overlap: one was inside the other's window,
	// waiting on the lock. Without this the assertions below could be about
	// two passes that never met.
	if !starts[0].Before(ends[1]) || !starts[1].Before(ends[0]) {
		t.Errorf("the passes did not overlap (%v..%v and %v..%v), so nothing here was contended",
			starts[0], ends[0], starts[1], ends[1])
	}

	// The store is what one pass would have left: every session once.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != sessions+1 {
		t.Errorf("the store holds %d sessions after two passes, want %d", len(m.Sessions), sessions+1)
	}
	counted := 0
	for _, meta := range m.Sessions {
		counted += meta.Counted
	}
	if counted != sessions+1 {
		t.Errorf("the sessions count %d messages between them, want %d", counted, sessions+1)
	}

	// And the state one pass left behind does not move the next one.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	after, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Sessions) != sessions+1 {
		t.Errorf("a pass after the two holds %d sessions, want %d", len(after.Sessions), sessions+1)
	}
	countedAfter := 0
	for _, meta := range after.Sessions {
		countedAfter += meta.Counted
	}
	if countedAfter != counted {
		t.Errorf("the counts moved from %d to %d with nothing new to read", counted, countedAfter)
	}
}
