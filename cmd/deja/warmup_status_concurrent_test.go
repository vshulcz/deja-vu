package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// "Written whole and renamed: a reader must never see half a record" is the
// invariant on the status file. It held for one writer and not for two — and
// two is ordinary, since two agents starting together each spawn a detached
// build. Both derived the temp name from the destination, so one truncated what
// the other was renaming: 180 of 18049 reads came back unparseable (#1319).
func TestTwoBuildsNeverPublishHalfAStatus(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := warmupStatusPath(dir)

	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			f := &fileProgress{path: p}
			for i := 0; i < 400 && !stop.Load(); i++ {
				f.st = warmupStatus{Phase: "claude", Done: i * (w + 1), Total: i * 100, Stores: 7}
				f.flush(true)
			}
		}(w)
	}
	var reads, empty, bad int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			atomic.AddInt64(&reads, 1)
			if len(raw) == 0 {
				atomic.AddInt64(&empty, 1)
				continue
			}
			var got warmupStatus
			if json.Unmarshal(raw, &got) != nil {
				atomic.AddInt64(&bad, 1)
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
	if reads == 0 {
		t.Fatal("the reader never saw the file, so this proves nothing")
	}
	// Every unparseable read tells a surface no build is running while one is:
	// readWarmupStatus returns nil on a record it cannot decode, and the agent
	// is handed silence in the one state the sentence exists for.
	if bad > 0 || empty > 0 {
		t.Errorf("%d of %d reads saw a torn status (%d empty) — two writers shared one temp name", bad+empty, reads, empty)
	}
}
