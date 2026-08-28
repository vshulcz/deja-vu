package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The events log takes concurrent writers with no lock on purpose: usage data
// is advisory and the hot path stays lock-free, so a rotation running against
// an append may drop that event (#1319). What is not on the table is a log that
// cannot be read: a half-written line reads as no history rather than as one
// lost event, and a duplicated event would inflate the numbers every surface
// quotes. This pins the part that has to hold (#2425).
//
// The log is seeded with events old enough to drop, because a rotation with
// nothing to drop returns before it writes anything (#1972) — without the seed
// this exercises appends alone and says nothing about a rewrite.
func TestTheEventsLogStaysReadableUnderWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	seeded := seedOldEvents(t, dir)

	wide := strings.Repeat("id", 400)
	const writers, each = 4, 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				RecordServedSessions(dir, KindSearch, 4000, 1, false, 40000,
					[]string{fmt.Sprintf("%s-w%d-%d", wide, w, i)})
			}
		}(w)
	}
	wg.Wait()

	body, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	stale := 0
	for i, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not a record deja can read: %v\n%.200s", i+1, err, line)
		}
		if strings.HasPrefix(e.Kind, "seed") {
			stale++
			continue
		}
		for _, id := range e.SessionIDs {
			seen[id]++
		}
	}
	// The premise: a rotation actually rewrote the file. Some of the seed is
	// expected to survive — when every event is past the window the newest
	// keepAtLeast are kept, so that a fortnight away does not empty the log
	// (#1922) — but the file is smaller than what was seeded.
	if fi, err := os.Stat(Path(dir)); err != nil {
		t.Fatal(err)
	} else if fi.Size() >= seeded {
		t.Fatalf("the log did not shrink (%d B seeded, %d B now), so no rewrite raced an append", seeded, fi.Size())
	}
	if stale > keepAtLeast {
		t.Errorf("%d events past the window survived, more than the %d floor", stale, keepAtLeast)
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("an event was written %d times: %.40s", n, id)
		}
	}
	// How much the trade actually costs, recorded rather than asserted: this
	// machine kept all 800, CI kept 200 and 362 of them on the two runners.
	// The comment in rotate says an event "may" be lost; against a rotation
	// that is rewriting, it is most of a burst. There is no bound to hold here
	// — the design promises none — and a floor written from one machine's
	// timing is a flake waiting for a slower one.
	t.Logf("%d of %d appended events survived a rotation running against them", len(seen), writers*each)
}

// seedOldEvents fills the log past the rotation threshold with events dated
// beyond the keep window, so the next append has something to drop.
func seedOldEvents(t *testing.T, dir string) int64 {
	t.Helper()
	old := time.Now().UTC().Add(-keepWindow - 48*time.Hour)
	var buf bytes.Buffer
	for i := 0; buf.Len() < rotateAt+(1<<16); i++ {
		e := Event{Time: old.Add(time.Duration(i) * time.Second), Kind: "seed", Bytes: 4000,
			Sessions: 1, SessionIDs: []string{strings.Repeat("s", 800) + fmt.Sprint(i)}}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(b, '\n'))
	}
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return int64(buf.Len())
}
