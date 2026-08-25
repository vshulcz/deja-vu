package peers

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// Record reads the list, edits it and writes it back. Nothing serialised the
// three steps, so two syncs finishing at once — two terminals, or a hook beside
// a `deja sync` — kept only the last writer's row. A lost row is a machine deja
// stops syncing with, and nothing says so (#1883).
func TestConcurrentRecordsKeepEveryMachine(t *testing.T) {
	writePeers(t, `{"peers":[]}`)
	hosts := []string{"laptop", "server", "mini", "build-box", "desktop"}
	when := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	errs := make([]error, len(hosts))
	for i, h := range hosts {
		wg.Add(1)
		go func(i int, h string) {
			defer wg.Done()
			errs[i] = Record(h, false, when, nil)
		}(i, h)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("%s: %v", hosts[i], err)
		}
	}

	got := Load()
	if len(got) != len(hosts) {
		var names []string
		for _, p := range got {
			names = append(names, p.Host)
		}
		t.Fatalf("%d machines survived %d concurrent records: %s", len(got), len(hosts), strings.Join(names, ", "))
	}
	for _, p := range got {
		if !p.LastPush.Equal(when) {
			t.Errorf("%s kept no exchange: %#v", p.Host, p)
		}
	}
}

// A row written by one sync must survive another sync recording a different
// machine — the read-modify-write case, rather than a fresh file.
func TestARecordDoesNotDropTheRowsItDidNotWrite(t *testing.T) {
	writePeers(t, `{"peers":[{"host":"laptop","last_push":"2026-08-20T10:00:00Z"}]}`)
	when := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for _, h := range []string{"server", "mini"} {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			_ = Record(h, true, when, nil)
		}(h)
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, p := range Load() {
		seen[p.Host] = true
	}
	for _, want := range []string{"laptop", "server", "mini"} {
		if !seen[want] {
			t.Errorf("%s is gone from the list", want)
		}
	}
}

// The lock waits two seconds and then writes anyway, so that a sync is never
// failed for want of it (#1884) — which is the old lost-update behaviour if
// contention ever reaches that far. It does not: one uncontended Record takes
// 298–322µs whether the file holds one row or sixteen, so the writers here
// queue a few milliseconds in front of each other, and 128 of them measured
// ~40ms. Reaching two seconds would take thousands at once, on a file deja
// writes once per exchange.
//
// Goroutines rather than processes, which is what a test can do here: this
// pins that the read-modify-write is serialised, not that the lock file works
// between processes.
func TestSustainedContentionKeepsEveryMachine(t *testing.T) {
	writePeers(t, `{"peers":[]}`)
	const writers, each = 16, 20
	when := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		host := fmt.Sprintf("host-%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < each; k++ {
				if err := Record(host, k%2 == 0, when, nil); err != nil {
					t.Errorf("%s: %v", host, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	list := Load()
	if len(list) != writers {
		t.Fatalf("%d machines survived %d records from %d writers", len(list), writers*each, writers)
	}
	for _, p := range list {
		if p.LastPush.IsZero() || p.LastPull.IsZero() {
			t.Errorf("%s lost one of its two directions: %#v", p.Host, p)
		}
	}
}
