package index

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(40 * time.Millisecond)
		fresh := filepath.Join(dir, "tmp.db")
		if err := os.MkdirAll(fresh, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(fresh, "manifest.gob"), []byte("rebuilt"), 0o644)
		_ = os.Rename(fresh, index)
		_ = os.RemoveAll(parked)
	}()

	f, err := openIndexFile(path)
	if err != nil {
		t.Fatalf("a reader in the swap window was handed %v", err)
	}
	defer func() { _ = f.Close() }()
	wg.Wait()
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 4; i++ {
			writeSession(t, proj, "d", "the kafka consumer keeps rebalancing")
			_ = Ensure(dir, "", true, nil)
		}
		close(stop)
	}()
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
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(40 * time.Millisecond)
		_ = os.Rename(parked, idx)
	}()
	f, err := openIndexFile(path)
	if err != nil {
		t.Fatalf("a bucket read in the swap window was handed %v", err)
	}
	_ = f.Close()
	wg.Wait()
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
