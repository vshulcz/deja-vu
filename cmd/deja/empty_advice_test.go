package main

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmptyIndexHintPointsAtTheRightNextStep(t *testing.T) {
	// Hermetic env: no store has any files, so the honest answer is that there
	// is no history here — not that the index needs building, which every path
	// reaching this message has just done.
	hermeticEnv(t)
	got := emptyIndexHint("nothing indexed yet")
	if !strings.Contains(got, "no agent history was found") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "run `deja index`") {
		t.Fatalf("advising a rebuild after a rebuild sends the reader in a circle: %q", got)
	}
	if !strings.Contains(got, "deja sources") {
		t.Fatalf("the reader needs somewhere to go: %q", got)
	}
}

func TestParseDurAcceptsWhatDejaDocuments(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30d": 30 * 24 * time.Hour,
		"12h": 12 * time.Hour,
		"90m": 90 * time.Minute,
		"45s": 45 * time.Second,
	} {
		got, err := parseDur(in)
		if err != nil || got != want {
			t.Errorf("parseDur(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestParseDurExplainsItselfOnJunk(t *testing.T) {
	for _, in := range []string{"abc", "7x", "", "d", "-", "30 d"} {
		_, err := parseDur(in)
		if err == nil {
			t.Errorf("parseDur(%q) should fail", in)
			continue
		}
		// time.ParseDuration's own wording names Go's syntax and never mentions
		// days, which is the unit this flag is usually given.
		if strings.Contains(err.Error(), "time: invalid duration") {
			t.Errorf("parseDur(%q) leaked the stdlib message: %v", in, err)
		}
		if !strings.Contains(err.Error(), "30d") {
			t.Errorf("parseDur(%q) should say what is accepted: %v", in, err)
		}
	}
}

func TestEnsureErrorSaysWhatToChange(t *testing.T) {
	// A denied write surfaced as `ensure: open /…/index.db.lock: permission
	// denied` — an internal lock path and a syscall error, neither of which
	// tells the reader what to do.
	// The directory has to be one that exists: a path whose parent is gone is
	// a disk that was unmounted, which says something else entirely (#931).
	dir := filepath.Join(t.TempDir(), "index.db")
	err := ensureError(dir, fs.ErrPermission)
	if err == nil {
		t.Fatal("want an error")
	}
	got := err.Error()
	for _, want := range []string{dir, "permissions", "DEJA_INDEX_DIR"} {
		if !strings.Contains(got, want) {
			t.Errorf("message should mention %q: %s", want, got)
		}
	}
	if strings.Contains(got, ".lock") {
		t.Errorf("the lock file is deja's business, not the reader's: %s", got)
	}
	// Anything else keeps its original wording rather than being guessed at.
	other := ensureError("/x", errors.New("disk on fire"))
	if !strings.Contains(other.Error(), "disk on fire") {
		t.Errorf("unrelated failures must not be rewritten: %v", other)
	}
}
