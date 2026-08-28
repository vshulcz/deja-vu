package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A write is never refused for want of the lock: every way of failing to take
// it ends in the write happening anyway (#2413).
func TestTheLogLockNeverRefusesAWrite(t *testing.T) {
	dir := t.TempDir()

	t.Run("a lock left by a dead process is taken over", func(t *testing.T) {
		p := filepath.Join(dir, "stale.jsonl")
		lock := p + ".lock"
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-logLockStale - time.Second)
		if err := os.Chtimes(lock, old, old); err != nil {
			t.Fatal(err)
		}
		ran := false
		withLogLock(p, func() { ran = true })
		if !ran {
			t.Error("a stale lock stopped the write")
		}
		if _, err := os.Stat(lock); err == nil {
			t.Error("the lock outlived the write that took it over")
		}
	})

	t.Run("a lock somebody is holding does not stop the write", func(t *testing.T) {
		p := filepath.Join(dir, "held.jsonl")
		lock := p + ".lock"
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(lock) }()
		start := time.Now()
		ran := false
		withLogLock(p, func() { ran = true })
		if !ran {
			t.Error("the write was dropped rather than raced")
		}
		if waited := time.Since(start); waited < logLockPoll {
			t.Errorf("it did not wait at all: %s", waited)
		}
		if _, err := os.Stat(lock); err != nil {
			t.Error("the writer removed a lock it never took")
		}
	})

	t.Run("a directory that cannot hold a lock", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes anywhere")
		}
		ro := filepath.Join(dir, "readonly")
		if err := os.MkdirAll(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		ran := false
		withLogLock(filepath.Join(ro, "log.jsonl"), func() { ran = true })
		if !ran {
			t.Error("a directory deja cannot lock stopped the write")
		}
	})
}
