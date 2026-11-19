package index

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A rebuild parks the old index as <dir>.old and clears it afterwards (#181).
// A leftover that cannot be removed — an interrupted swap whose directory came
// back read-only — failed every later rebuild with `rename …: file exists`:
// two paths nobody chose and nothing to do about either (#1009).
func TestALeftoverSwapDirectoryIsNamedInsteadOfTheRename(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	newer := filepath.Join(tmp, "index.db.tmp")
	for _, d := range []string{dir, newer} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "records.bin"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The ordinary swap: the leftover is cleared and the new index lands.
	if err := swapIndexDir(dir, newer); err != nil {
		t.Fatalf("an ordinary swap failed: %v", err)
	}
	if _, err := os.Stat(dir + ".old"); err == nil {
		t.Error("the parking directory outlived the swap")
	}

	// A leftover deja cannot remove.
	old := dir + ".old"
	if err := os.MkdirAll(filepath.Join(old, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(old, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(old, 0o700) })
	if err := os.MkdirAll(newer, 0o755); err != nil {
		t.Fatal(err)
	}

	err := swapIndexDir(dir, newer)
	if err == nil {
		t.Fatal("the swap claimed to succeed over a leftover it cannot replace")
	}
	if strings.Contains(err.Error(), "rename") || strings.Contains(err.Error(), "file exists") {
		t.Errorf("the failure is still the raw syscall: %v", err)
	}
	if !strings.Contains(err.Error(), old) || !strings.Contains(err.Error(), "remove that directory") {
		t.Errorf("the failure does not say what to remove: %v", err)
	}
	// The index that was there is still there — nothing was lost to the failed
	// swap.
	if _, statErr := os.Stat(filepath.Join(dir, "records.bin")); statErr != nil {
		t.Errorf("the previous index did not survive the failed swap: %v", statErr)
	}
}

// The selector the refusal uses to name what it would restore: content keys
// ride along with their session and must not be listed as sessions of their
// own (#1014).
func TestTombstonesMatchingNamesSessionsOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	set := map[string]bool{
		"claude:sess0":                        true,
		"claude:sess1":                        true,
		"claude:alpha":                        true,
		"claude:sess0\x00deja-note-day:x:1:2": true,
	}
	if err := writeTombstones(set); err != nil {
		t.Fatal(err)
	}
	got := TombstonesMatching("claude:sess")
	want := []string{"claude:sess0", "claude:sess1"}
	if len(got) != len(want) {
		t.Fatalf("matching keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("matching keys = %v, want %v", got, want)
			break
		}
	}
	if n := TombstoneMatches("claude:sess"); n != len(want) {
		t.Errorf("count %d disagrees with the names %v", n, got)
	}
	if got := TombstonesMatching("claude:nothing"); len(got) != 0 {
		t.Errorf("a selector that matches nothing named %v", got)
	}
	if !Tombstoned("claude:alpha") || Tombstoned("claude:absent") {
		t.Error("Tombstoned disagrees with what was written")
	}
}

// A build killed mid-flight leaves <dir>.tmp behind. Only `index --rebuild`
// cleared it, so an ordinary run over a fresh index walked past a full index
// worth of bytes and doctor reported only the live index's size.
func TestAStaleBuildScratchDirIsSweptOnAFreshIndex(t *testing.T) {
	dir, _ := deepFixture(t)
	before, err := DeepVerify(dir)
	if err != nil || !before.Clean() {
		t.Fatalf("baseline: %v %+v", err, before.Findings)
	}
	scratch := dir + ".tmp"
	if err := os.MkdirAll(filepath.Join(scratch, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "records.bin"), []byte("half a build"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ensure decides the index is fresh and returns without building.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("Ensure left the scratch dir behind: %v", err)
	}

	// And the same via the standalone sweep `deja index` calls before it
	// decides not to build at all.
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	SweepStaleTmp(dir)
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("SweepStaleTmp left the scratch dir behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatalf("the live index did not survive the sweep: %v", err)
	}
}

// heldOpen is the refusal Windows gives while a handle inside the directory is
// still open, so a test can stand where Windows is without needing it.
func heldOpen(from, to string) error {
	return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.Errno(32)}
}

// swapClock stands the swap on a clock that moves only when the swap sleeps,
// and returns how far it has moved. What a test bounds is the waiting the swap
// chose, not how long a loaded runner took to do it: the same swap measured
// 306ms against a 284ms bound on ubuntu-latest (#4160).
func swapClock(t *testing.T) func() time.Duration {
	t.Helper()
	start := time.Unix(0, 0)
	var waited time.Duration
	wasNow, wasSleep := swapNow, swapSleep
	swapNow = func() time.Time { return start.Add(waited) }
	swapSleep = func(d time.Duration) { waited += d }
	t.Cleanup(func() { swapNow, swapSleep = wasNow, wasSleep })
	return func() time.Duration { return waited }
}

// onWindows makes the wait apply on the machine running the test.
func onWindows(t *testing.T) {
	t.Helper()
	was := renameOS
	renameOS = "windows"
	t.Cleanup(func() { renameOS = was })
}

// Windows refuses to rename a directory while a handle inside it is open, so
// two ordinary passes at once left the loser unable to swap and the store a
// session short (#2228). The reader it is waiting on is finishing a read, so
// the swap waits it out rather than giving up on the pass.
func TestASwapWaitsOutARefusedRename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "records.bin"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	onWindows(t)
	var refusals atomic.Int32
	refusals.Store(3)
	orig := renameFile
	renameFile = func(from, to string) error {
		if refusals.Add(-1) >= 0 {
			return heldOpen(from, to)
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renameFile = orig })

	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatalf("the swap gave up on a rename that was only being held: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "new" {
		t.Errorf("the new index is not in place: %q %v", b, err)
	}
	if _, err := os.Stat(dir + ".old"); err == nil {
		t.Error("the parking spot was left behind")
	}
}

// A rename refused for the whole wait is a failure the pass reports, with the
// previous index still where it was: leaving nothing there is worse than
// leaving what the reader already had.
func TestASwapThatCannotRenameKeepsTheOldIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	onWindows(t)
	var held atomic.Int32
	held.Store(2)
	orig := renameFile
	renameFile = func(from, to string) error {
		// Every rename costs a little on a loaded runner, which is time the
		// swap did not choose to spend (#4160).
		time.Sleep(20 * time.Millisecond)
		// The second rename is refused for good; the restore is refused twice
		// and then allowed, which is the shape a swap meets when the same
		// handles are holding both.
		switch filepath.Base(from) {
		case "index.db.tmp":
			return heldOpen(from, to)
		case "index.db.old":
			if held.Add(-1) >= 0 {
				return heldOpen(from, to)
			}
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renameFile = orig })

	away := swapClock(t)
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("a rename refused for the whole wait was reported as a swap")
	}
	// Bounded by what a reader will wait for it, not by "eventually": the
	// failed rename and the restore share that window, with a floor under the
	// restore so it is never left none of it. Counted on the swap's own clock,
	// so neither a windows sleep rounding up to the scheduler tick (#3648) nor
	// a loaded runner (#4160) moves it.
	bound := swapRenameWait + restoreRenameFloor + swapRenameStep
	if took := away(); took > bound {
		t.Errorf("the index was away for %v, past the %v it may take", took, bound)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "old" {
		t.Errorf("the previous index is not where it was: %q %v", b, err)
	}
}

// The second rename is the one that matters to a reader: between it and the
// parking step the index is not where readers look, so a swap that waits there
// has to be done inside the window a reader waits for it (swapWindowTries ×
// swapWindowWait). This is that path — refused, then allowed.
func TestASwapWaitsOutTheSecondRenameWithinTheReadersWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "records.bin"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	onWindows(t)
	var refusals atomic.Int32
	refusals.Store(3)
	orig := renameFile
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "index.db.tmp" && refusals.Add(-1) >= 0 {
			return heldOpen(from, to)
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renameFile = orig })

	away := swapClock(t)
	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatalf("the swap gave up on the rename readers were holding: %v", err)
	}
	if took := away(); took > swapRenameWait {
		t.Errorf("the index was away for %v, past the %v a reader waits", took, swapRenameWait)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "new" {
		t.Errorf("the new index is not in place: %q %v", b, err)
	}
}

// A refusal that will not clear — a read-only filesystem, a path that is a
// file — is reported at once rather than waited out: the reader has to act on
// it either way.
func TestASwapDoesNotWaitOutARefusalThatWillNotClear(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	onWindows(t)
	orig := renameFile
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "index.db.tmp" {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EROFS}
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renameFile = orig })

	away := swapClock(t)
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("a read-only filesystem was reported as a swap")
	}
	if took := away(); took > 0 {
		t.Errorf("waited %v on a refusal that cannot clear", took)
	}
	// And the previous index is back: a fail-fast is still a swap that did not
	// happen, not one that took the index with it.
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "old" {
		t.Errorf("the previous index is not where it was: %q %v", b, err)
	}
}

// And a refusal that is not an errno at all — whatever a filesystem driver or
// a test double hands back — is reported rather than waited out.
func TestASwapDoesNotWaitOutAnUnrecognisedRefusal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	onWindows(t)
	orig := renameFile
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "index.db.tmp" {
			return errors.New("the volume was dismounted")
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renameFile = orig })

	away := swapClock(t)
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("an unrecognised refusal was reported as a swap")
	}
	if took := away(); took > 0 {
		t.Errorf("waited %v on a refusal deja cannot read", took)
	}
	// And the previous index is back: a fail-fast is still a swap that did not
	// happen, not one that took the index with it.
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "old" {
		t.Errorf("the previous index is not where it was: %q %v", b, err)
	}
}

// The parking step is the rename nobody is waiting on — the index is still
// where readers look until it lands — so it waits far past the window that
// bounds the pair after it. That is the case #2228 is about: a pass that spent
// seconds building the new index must not give up because a search held a
// handle for a quarter of a second.
func TestTheParkingStepWaitsPastTheReadersWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "records.bin"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	onWindows(t)
	// Past the reader's window by a margin, and well inside the parking step's.
	refusals := int32(swapRenameWait/swapRenameStep) + 5
	var left atomic.Int32
	left.Store(refusals)
	orig := renameFile
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "index.db" && left.Add(-1) >= 0 {
			return heldOpen(from, to)
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renameFile = orig })

	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatalf("the swap gave up on the parking step: %v", err)
	}
	if left.Load() >= 0 {
		t.Fatalf("the parking step was never held for the whole window: %d refusals left", left.Load())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "new" {
		t.Errorf("the new index is not in place: %q %v", b, err)
	}
}

// The manifest is renamed over while readers decode it, and Windows refuses
// that rename for as long as one of them has the file open. The write gave up
// on the first refusal: a recall beside a remember failed with "Access is
// denied" on windows CI.
func TestAManifestWriteWaitsOutAReader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.gob")
	if err := writeGobAtomic(p, manifestCore{Version: 1}); err != nil {
		t.Fatal(err)
	}

	onWindows(t)
	swapClock(t)
	var refusals atomic.Int32
	refusals.Store(3)
	orig := renameFile
	renameFile = func(from, to string) error {
		if refusals.Add(-1) >= 0 {
			return heldOpen(from, to)
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renameFile = orig })

	if err := writeGobAtomic(p, manifestCore{Version: 2}); err != nil {
		t.Fatalf("the write gave up on a rename a reader was only holding: %v", err)
	}
	if refusals.Load() >= 0 {
		t.Fatalf("the write never met the refusals: %d left", refusals.Load())
	}
	var got manifestCore
	if err := readGob(p, &got); err != nil || got.Version != 2 {
		t.Errorf("manifest = %+v, %v; want the new one in place", got, err)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("the temp file was left behind")
	}
}

// The same on a real handle, which only Windows refuses: the reader closes
// while the writer is waiting, and the write lands.
func TestAManifestWriteLandsAfterARealReaderCloses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.gob")
	if err := writeGobAtomic(p, manifestCore{Version: 1}); err != nil {
		t.Fatal(err)
	}
	r, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- writeGobAtomic(p, manifestCore{Version: 2}) }()
	time.Sleep(50 * time.Millisecond)
	_ = r.Close()
	if err := <-done; err != nil {
		t.Fatalf("the write failed behind a reader that closed: %v", err)
	}
	var got manifestCore
	if err := readGob(p, &got); err != nil || got.Version != 2 {
		t.Errorf("manifest = %+v, %v; want the new one in place", got, err)
	}
}

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
	old := waitOutSwapWindow
	waitOutSwapWindow = func() {
		_ = os.Rename(parked, dir)
	}
	defer func() { waitOutSwapWindow = old }()
	if !HasManifest(dir) {
		t.Error("a hook landing in the swap window was told there is no index")
	}
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
	// The store agrees with its manifest again by the time the second read
	// happens. Standing where the wait is rather than racing a goroutine
	// against it: on a loaded runner a 5 ms sleep can land after the 20 ms
	// window and the test failed on a store that behaved exactly as designed
	// (#1782).
	restored := false
	old := waitOutSwapWindow
	waitOutSwapWindow = func() {
		restored = true
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Error(err)
		}
	}
	defer func() { waitOutSwapWindow = old }()

	if Damaged(dir) {
		t.Error("a store that agrees with its manifest a moment later was called damaged")
	}
	if !restored {
		t.Error("the second read never happened, so this proved nothing")
	}
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

// Both retry loops take the same pause by the same name: a test that stands
// where the wait is has to see every path that waits, or the next one to grow a
// direct time.Sleep is the one that goes back to racing the scheduler (#1782).
func TestEveryRetryLoopTakesTheSamePause(t *testing.T) {
	src, err := os.ReadFile("swap_window.go")
	if err != nil {
		t.Fatal(err)
	}
	// The definition of the pause is allowed to sleep; nothing else is.
	for i, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "time.Sleep(swapWindowWait)") {
			continue
		}
		if strings.Contains(line, "var waitOutSwapWindow") {
			continue
		}
		t.Errorf("swap_window.go:%d sleeps directly instead of going through waitOutSwapWindow: %s", i+1, strings.TrimSpace(line))
	}
}

// A rebuild is two renames and the index directory does not exist between
// them. Readers that land there used to get a raw ENOENT naming a file they
// never chose, during an operation the user started on purpose (#1317).
func TestReadersWaitOutTheSwapWindow(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "index.db")
	if err := os.MkdirAll(index, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(index, "manifest.gob")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stand in the window: the previous index is parked, the new one arrives a
	// moment later — exactly what swapIndexDir does.
	parked := index + ".old"
	if err := os.Rename(index, parked); err != nil {
		t.Fatal(err)
	}
	old := waitOutSwapWindow
	waitOutSwapWindow = func() {
		fresh := filepath.Join(dir, "tmp.db")
		if err := os.MkdirAll(fresh, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(fresh, "manifest.gob"), []byte("rebuilt"), 0o644)
		_ = os.Rename(fresh, index)
		_ = os.RemoveAll(parked)
	}
	defer func() { waitOutSwapWindow = old }()

	f, err := openIndexFile(path)
	if err != nil {
		t.Fatalf("a reader in the swap window was handed %v", err)
	}
	_ = f.Close()
}

// And an index that is simply not there keeps failing at once: that path has a
// message of its own, and waiting for a rebuild nobody started would only make
// it slower.
func TestAMissingIndexStillFailsImmediately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.db", "manifest.gob")
	start := time.Now()
	_, err := openIndexFile(path)
	if err == nil {
		t.Fatal("opened a file that does not exist")
	}
	if !os.IsNotExist(err) {
		t.Errorf("the error stopped saying the file is missing: %v", err)
	}
	if took := time.Since(start); took > swapWindowWait {
		t.Errorf("a missing index cost %v waiting for a swap that was not happening", took)
	}
}

// The whole point is the message a person sees. Under a rebuild, a search must
// not answer with an internal path.
func TestSearchDoesNotNameAnInternalPathDuringARebuild(t *testing.T) {
	dir, proj := swapFixture(t)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 4 {
			writeSession(t, proj, "d", "the kafka consumer keeps rebalancing")
			_ = Ensure(dir, "", true, nil)
		}
		close(stop)
	})
	var bad []string
	for done := false; !done; {
		select {
		case <-stop:
			done = true
		default:
		}
		if _, err := Recent(dir, 5); err != nil && strings.Contains(err.Error(), "no such file") {
			bad = append(bad, err.Error())
		}
	}
	wg.Wait()
	if len(bad) > 0 {
		t.Errorf("%d reads during a rebuild reported a missing internal file, e.g. %s", len(bad), bad[0])
	}
}

func swapFixture(t *testing.T) (dir, proj string) {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	proj = filepath.Join(claude, "-swap")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "a", "why does the retry queue stall")
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir, proj
}

// The postings live one level below the index directory, so deriving the
// parking spot from the file's own parent looked for "buckets.old" and gave up
// at once — on the readers that matter most during a search.
func TestBucketReadsWaitOutTheSwapWindowToo(t *testing.T) {
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.db")
	if err := os.MkdirAll(filepath.Join(idx, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(idx, "buckets", "0.bin")
	if err := os.WriteFile(path, []byte("postings"), 0o644); err != nil {
		t.Fatal(err)
	}
	parked := idx + ".old"
	if err := os.Rename(idx, parked); err != nil {
		t.Fatal(err)
	}
	old := waitOutSwapWindow
	waitOutSwapWindow = func() {
		_ = os.Rename(parked, idx)
	}
	defer func() { waitOutSwapWindow = old }()
	f, err := openIndexFile(path)
	if err != nil {
		t.Fatalf("a bucket read in the swap window was handed %v", err)
	}
	_ = f.Close()
}

// A crashed swap leaves the parking spot behind until the next build clears it.
// On a machine whose index is otherwise fine, that must not tax every read with
// the full wait.
func TestALeftoverParkingSpotCostsNothing(t *testing.T) {
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.db")
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(idx+".old", 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := openIndexFile(filepath.Join(idx, "gone.bin")); err == nil {
		t.Fatal("opened a file that does not exist")
	}
	if took := time.Since(start); took > swapWindowWait {
		t.Errorf("a missing file cost %v beside a leftover .old directory", took)
	}
}
