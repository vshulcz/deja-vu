package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// countingStoreChecks hands deniedStoreCount the real checks with every parser
// wrapped in a counter.
func countingStoreChecks(t *testing.T) *int {
	t.Helper()
	calls := 0
	old := deniedStoreChecks
	deniedStoreChecks = func() []doctorStoreCheck {
		checks := old()
		for i := range checks {
			parse := checks[i].parse
			checks[i].parse = func(p string) ([]model.Session, error) {
				calls++
				return parse(p)
			}
		}
		return checks
	}
	t.Cleanup(func() { deniedStoreChecks = old })
	return &calls
}

func writeClaudeTurn(t *testing.T, path, id, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`{"type":"user","sessionId":"` + id + `","cwd":"/w","timestamp":"2026-08-08T01:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
}

// Asking whether a store can be opened is not asking what is in it. The probe
// ran doctor's parser over the newest file of every store, so on one big
// SQLite store a one-message `deja index` read the whole store a second time
// after the pass that had just read it (#4272).
func TestDeniedStoreCountParsesNothing(t *testing.T) {
	hermeticEnv(t)
	writeClaudeTurn(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj", "a.jsonl"), "a1", "flumberjack")
	calls := countingStoreChecks(t)

	if n := deniedStoreCount(); n != 0 {
		t.Fatalf("denied = %d, want 0", n)
	}
	if *calls != 0 {
		t.Errorf("the denied-store probe ran %d parser call(s); it only needs to open the store", *calls)
	}
	// Control: the wrapper does count, so zero above is not vacuous.
	for _, c := range deniedStoreChecks() {
		if c.name == "claude" {
			inspectDoctorStore(c)
		}
	}
	if *calls == 0 {
		t.Fatal("doctor's own inspection parsed nothing either; the counter is not wired")
	}
}

// The probe still finds a file it may not open.
func TestDeniedStoreCountStillSeesALockedFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	hermeticEnv(t)
	locked := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj", "locked.jsonl")
	writeClaudeTurn(t, locked, "l1", "flumberjack")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot lock a file here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })
	if f, err := os.Open(locked); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}
	calls := countingStoreChecks(t)
	if n := deniedStoreCount(); n != 1 {
		t.Errorf("denied = %d, want 1", n)
	}
	if *calls != 0 {
		t.Errorf("%d parser call(s) for a store that cannot be opened", *calls)
	}
}

// An incremental pass leaves LastBuild unset, which the empty-index branch read
// as "nothing was indexed": every one-message `deja index` ran the denied-store
// probe through each store's parser, and with any store locked told a full
// index it had nothing to index yet (#4272).
func TestIncrementalIndexParsesNoStoreOrCallsTheIndexEmpty(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	hermeticEnv(t)
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj", "a.jsonl")
	writeClaudeTurn(t, transcript, "a1", "flumberjack")
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	// A locked store elsewhere, so the empty-index branch has something to say.
	locked := filepath.Join(os.Getenv("DEJA_GEMINI_ROOT"), "tmp")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot lock a directory here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("this filesystem ignores the mode")
	}

	writeClaudeTurn(t, transcript, "a1", "second turn")
	// A fresh process: the in-process first run left its summary behind.
	index.LastBuild = index.BuildSummary{}
	calls := countingStoreChecks(t)
	out, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "nothing to index") {
		t.Errorf("an incremental pass over an index with a session in it says it is empty:\n%s", out)
	}
	if *calls != 0 {
		t.Errorf("an incremental `deja index` ran %d store parser call(s) after the pass", *calls)
	}
}
