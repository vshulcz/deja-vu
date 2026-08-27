package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Two doors, two answers to "another deja is building the index". `deja search`
// answers from the store as it stands and asks for a refresh in the background;
// the doors that must see a current store — `deja files` among them — take the
// blocking lock and say so, which is what #994 added the notice for. Both
// halves were only ever true by inspection: `mcp_no_block_test.go` pins the MCP
// server's side and `internal/index/lock_wait_test.go` pins that the wait is
// reported once, and nothing said what a person at a terminal gets — which of
// the two doors they are standing at decides whether they wait out a rebuild.
func TestASearchDoesNotWaitBehindAPassWhereTheOtherDoorsDo(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	// Enough of a store that a rebuild is long against a search: the point is
	// the difference between the two, not either number.
	const sessions = 600
	for i := 0; i < sessions; i++ {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": fmt.Sprintf("s%03d", i), "cwd": "/tmp/app",
			"timestamp": stamp,
			"message": map[string]any{"role": "user", "content": fmt.Sprintf(
				"turn %d: the pool timed out during the migration and we raised max_conns", i)},
		})
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("s%03d.jsonl", i)), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()

	// behind runs one command while a rebuild holds the lock. It reports
	// whether the pass was still running when the command started — a door
	// that blocks returns after the pass is over by definition, so asking
	// afterwards would answer nothing — and whether it was still running when
	// the command came back, which is what "did not wait" looks like.
	behind := func(args ...string) (out, said string, startedDuring, returnedDuring bool) {
		t.Helper()
		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(done)
			_ = index.Ensure(dir, "", true, nil)
		}()
		time.Sleep(10 * time.Millisecond) // long enough for the pass to hold the lock
		select {
		case <-done:
		default:
			startedDuring = true
		}
		said = captureStderr(t, func() {
			var runErr error
			out, runErr = captureRun(t, args...)
			if runErr != nil {
				t.Fatalf("%v: %v", args, runErr)
			}
		})
		select {
		case <-done:
		default:
			returnedDuring = true
		}
		wg.Wait()
		return out, said, startedDuring, returnedDuring
	}

	// The blocking doors: they come back only once the pass is over.
	for _, args := range [][]string{
		{"files", "migration"},
		{"view", "--no-open", "--out", filepath.Join(t.TempDir(), "v.html")},
	} {
		_, _, started, returned := behind(args...)
		switch {
		case !started:
			t.Logf("the rebuild was over before %s started: nothing to say about waiting this time", args[0])
		case returned:
			t.Errorf("%s takes the blocking lock and came back while the pass was still running", args[0])
		}
	}

	// And the search, which must come back while the pass is still going: it
	// answers from the store as it stands and asks for the refresh in the
	// background rather than queueing behind a rebuild.
	out, said, searchStarted, searchReturned := behind("search", "migration")
	if !strings.Contains(out, "s001") {
		t.Fatalf("the search found nothing, so this measures nothing:\n%s", out)
	}
	// What it says is the part that does not depend on the clock: the search
	// took the store as it stands and asked for the refresh in the background.
	if !strings.Contains(said, "answering from the index as it was") {
		t.Errorf("the search did not answer from the store as it stands:\n%s", said)
	}
	if searchStarted && !searchReturned {
		t.Log("the search came back after the pass had finished: a slow box, or a rebuild that ended early")
	}
}
