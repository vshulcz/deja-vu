package index

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// "Is there an index" is the first question every hook asks, and during a
// rebuild's swap the answer was no. The hook then asked for a rebuild that was
// already running and served the prompt without recall — memory lost because a
// directory was missing for a millisecond (#1319).
func TestHasManifestWaitsOutTheSwap(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.gob", "sessions.gob"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	parked := dir + ".old"
	if err := os.Rename(dir, parked); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(40 * time.Millisecond)
		_ = os.Rename(parked, dir)
	}()
	if !HasManifest(dir) {
		t.Error("a hook landing in the swap window was told there is no index")
	}
	wg.Wait()
}

// An index that is simply not there still answers at once: waiting for a
// rebuild nobody started would make every first run slower.
func TestAMissingIndexAnswersImmediately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	start := time.Now()
	if HasManifest(dir) {
		t.Fatal("found an index that does not exist")
	}
	if took := time.Since(start); took > swapWindowWait {
		t.Errorf("a missing index cost %v", took)
	}
}

// Damaged compares the manifest against the record log, and a rebuild can land
// between those two reads: the manifest is the old one, records.bin the new
// one, their sizes disagree, and a healthy index reads as broken. Measured on
// the hook predicate during eight rebuilds, 19 of 13124 asks said damaged —
// and every one of them served a prompt with no recall.
//
// The disagreement is staged here rather than raced: records.bin is short when
// the check starts and whole a moment later, which is what the second read is
// for.
func TestDamagedRereadsAPairThatDisagreed(t *testing.T) {
	dir, body := damagedFixture(t)
	path := filepath.Join(dir, "records.bin")
	if err := os.WriteFile(path, body[:len(body)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	// A build holds the lock, which is the state that makes a disagreement
	// worth a second look rather than a verdict.
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		_ = os.WriteFile(path, body, 0o600)
	}()
	if Damaged(dir) {
		t.Error("a store that agrees with its manifest a moment later was called damaged")
	}
	wg.Wait()
}

// And a store that is really short stays damaged: the second read is a second
// look, not a second chance.
func TestRealDamageSurvivesTheReread(t *testing.T) {
	dir, body := damagedFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), body[:len(body)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if !Damaged(dir) {
		t.Error("a truncated record log was reported healthy")
	}
	// And answers at once: the second look is for a build that is running, and
	// with none running the verdict is already final. Every hook asks this.
	if took := time.Since(start); took >= swapWindowWait {
		t.Errorf("a damaged store with no build running cost %v to report", took)
	}
}

// damagedFixture builds a small index and hands back its record log.
func damagedFixture(t *testing.T) (dir string, records []byte) {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "a", "the retry queue stalls on staging")
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("the fixture is damaged before the test starts")
	}
	body, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, body
}
