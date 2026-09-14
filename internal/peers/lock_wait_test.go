package peers

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A held lock is waited out, not written over. The wait used to stop after two
// seconds and run the read-modify-write anyway, which on a slow box dropped
// two machines of sixteen from the list — the lost update the lock exists to
// prevent (#1883, #3558).
func TestAHeldLockIsWaitedOutRatherThanWrittenOver(t *testing.T) {
	path := writePeers(t, `{"peers":[{"host":"laptop","last_push":"2026-08-20T10:00:00Z"}]}`)
	lock := path + ".lock"
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	done := make(chan error, 1)
	go func() {
		done <- Record("newcomer", true, time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC), nil)
	}()

	// Longer than the two seconds the old wait gave up after, and far short of
	// lockStale, so the lock is still the live kind.
	select {
	case err := <-done:
		t.Fatalf("Record returned while the lock was held: %v", err)
	case <-time.After(3 * time.Second):
	}
	if b, err := os.ReadFile(path); err != nil || strings.Contains(string(b), "newcomer") {
		t.Fatalf("the file was written through a held lock: %s (err=%v)", b, err)
	}

	// Released, and the row lands.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Record never finished after the lock was released")
	}
	var hosts []string
	for _, p := range Load() {
		hosts = append(hosts, p.Host)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts = %v, want the machine that held the file and the one that waited", hosts)
	}
}

// A lock nobody releases is still taken over rather than waited on forever:
// the staleness rule is what bounds the wait, and it has to fire well inside
// lockWait or a dead process would stop a sync from recording itself.
func TestAnAbandonedLockIsTakenOver(t *testing.T) {
	path := writePeers(t, `{"peers":[]}`)
	lock := path + ".lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := Record("newcomer", true, time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("took %s to take over a lock that was already stale", took)
	}
	if got := Load(); len(got) != 1 || got[0].Host != "newcomer" {
		t.Fatalf("peers = %#v", got)
	}
}
