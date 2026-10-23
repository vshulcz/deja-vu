package atomicfile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The point of the package: several writers at once, and a reader that only
// ever sees one of their payloads whole.
//
// The reader here opens the file again the moment it closes it, which is the
// most pressure the mix check can get and more than any poller of these files
// puts on them. On windows that grip can outlast the publish retry, and the
// rename is refused while the file is open: three of four writers were denied
// on CI (#5412). A refused publish leaves the old file whole, so it is counted
// here and not failed; TestWritesLandPastAPollingReader holds the promise that
// a write lands when the reader lets go between reads.
func TestConcurrentWritersNeverPublishAMix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	payloads := [][]byte{
		bytes.Repeat([]byte("a"), 4000),
		bytes.Repeat([]byte("b"), 4000),
		bytes.Repeat([]byte("c"), 4000),
		bytes.Repeat([]byte("d"), 4000),
	}
	if err := Write(path, payloads[0], 0o600); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var wg, writers sync.WaitGroup
	var landed, refused int64
	for w := range payloads {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 500 && !stop.Load(); i++ {
				err := Write(path, payloads[w], 0o600)
				switch {
				case err == nil:
					atomic.AddInt64(&landed, 1)
				case refusedWhileOpen(err):
					// Each refusal is the whole retry schedule; carrying on
					// would make this test minutes long on a slow runner.
					atomic.AddInt64(&refused, 1)
					return
				default:
					t.Error(err)
					return
				}
			}
		}(w)
	}
	var reads, mixed int64
	wg.Go(func() {
		for !stop.Load() {
			got, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			atomic.AddInt64(&reads, 1)
			ok := false
			for _, want := range payloads {
				if bytes.Equal(got, want) {
					ok = true
					break
				}
			}
			if !ok {
				atomic.AddInt64(&mixed, 1)
			}
		}
	})
	// The writers finish first, then the reader is told to stop: waiting on the
	// reader before setting the flag is a deadlock, which is how the first cut
	// of this test hung.
	writers.Wait()
	stop.Store(true)
	wg.Wait()
	if reads == 0 {
		t.Fatal("the reader never saw the file, so this proves nothing")
	}
	if mixed > 0 {
		t.Errorf("%d of %d reads saw neither payload whole", mixed, reads)
	}
	if landed == 0 {
		t.Fatalf("no write landed (%d refused), so the reads only ever saw the first payload", refused)
	}
	if refused > 0 {
		t.Logf("%d of %d publishes refused while the reader held the file", refused, landed+refused)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(payloads, func(p []byte) bool { return bytes.Equal(got, p) }) {
		t.Errorf("the file ended as neither payload whole (%d bytes)", len(got))
	}
	leftTemps(t, dir)
}

// What deja's readers actually do: read the file and let it go, then come back.
// Every write must land past one of those on every platform — the publish
// retry is there for exactly this reader on windows.
func TestWritesLandPastAPollingReader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	payloads := [][]byte{
		bytes.Repeat([]byte("a"), 4000),
		bytes.Repeat([]byte("b"), 4000),
	}
	if err := Write(path, payloads[0], 0o600); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var reads int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			if _, err := os.ReadFile(path); err == nil {
				atomic.AddInt64(&reads, 1)
			}
			// Far tighter than any surface polls the warmup status, which is
			// the busiest of these files.
			time.Sleep(5 * time.Millisecond)
		}
	}()
	var writers sync.WaitGroup
	for w := range payloads {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for range 100 {
				if err := Write(path, payloads[w], 0o600); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	writers.Wait()
	stop.Store(true)
	<-done
	if reads == 0 {
		t.Fatal("the reader never saw the file, so this proves nothing")
	}
	leftTemps(t, dir)
}

// Nothing is left behind on the way, and the mode is the one asked for.
func TestWriteLeavesNoTempAndKeepsTheMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	if err := Write(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	leftTemps(t, dir)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no permission bits to keep — chmod there toggles read-only
	// and nothing else, so the mode a caller asks for is a unix promise.
	if runtime.GOOS == "windows" {
		return
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode is %v, want 0600 — these files hold the user's own history", fi.Mode().Perm())
	}
	// A mode other than the one CreateTemp happens to give, so the chmod is
	// doing something rather than agreeing with the default by luck.
	other := filepath.Join(dir, "shared.jsonl")
	if err := Write(other, []byte("two\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(other); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode is %v, want 0640 — the caller's choice was dropped", fi.Mode().Perm())
	}
}

// A rename that cannot land must not leave the temp file on the way. The
// destination here is a directory, which CreateTemp is happy to write beside
// and rename can never replace.
func TestAFailedRenameLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("x"), 0o600); err == nil {
		t.Fatal("replacing a non-empty directory reported success")
	}
	leftTemps(t, dir)
}

// A destination whose directory does not exist fails and leaves nothing: the
// caller decides whether to make the directory, and a half-written temp file in
// some other directory would be litter it never looks for.
func TestWriteToAMissingDirectoryFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone", "status.json")
	if err := Write(path, []byte("x"), 0o600); err == nil {
		t.Error("writing into a missing directory reported success")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		t.Errorf("the failed write left %d entries behind", len(entries))
	}
}

// A process killed between creating the temp file and renaming it leaves one
// behind, and a unique name is never reused — so the next write in that
// directory clears the old ones.
func TestStaleTempsAreSwept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	stale := filepath.Join(dir, ".status.json.tmp-abandoned")
	if err := os.WriteFile(stale, []byte("half a record"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, ".status.json.tmp-inflight")
	if err := os.WriteFile(fresh, []byte("another writer, right now"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("an abandoned temp file survived: %v", err)
	}
	// A temp file that is being written right now must not be touched: it
	// belongs to another writer mid-flight.
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a live temp file was swept: %v", err)
	}
}

// WriteStream carries the embedding sidecar, megabytes of vectors that would
// cost twice the memory to buffer and hand over whole. It had no test of its
// own at all — 0% of the function — while Write beside it had five.
func TestWriteStreamPublishesWhatItWroteAndKeepsTheMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.bin")
	body := bytes.Repeat([]byte("vector"), 4096)

	if err := WriteStream(path, 0o640, func(w io.Writer) error {
		for off := 0; off < len(body); off += 512 {
			end := min(off+512, len(body))
			if _, err := w.Write(body[off:end]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("published %d bytes, wrote %d", len(got), len(body))
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 640 — CreateTemp makes 600 and the chmod is what fixes it", fi.Mode().Perm())
	}
	leftTemps(t, dir)
}

// A writer that gives up halfway must leave the old file alone. This is the
// whole reason the sidecar is written through this package: its header carries
// the vector count, so a half-written one decodes as nothing at all — worse
// than yesterday's, which decodes.
func TestWriteStreamPublishesNothingWhenTheWriterFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.bin")
	if err := os.WriteFile(path, []byte("the previous sidecar"), 0o600); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("out of vectors")
	err := WriteStream(path, 0o600, func(w io.Writer) error {
		if _, werr := w.Write(bytes.Repeat([]byte("half"), 1024)); werr != nil {
			return werr
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the writer's own error", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the previous sidecar" {
		t.Errorf("the failed write replaced the file with %q", got)
	}
	leftTemps(t, dir)
}

// Same rule as Write: a rename that cannot land leaves nothing on the way.
func TestWriteStreamLeavesNoTempWhenTheRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStream(path, 0o600, func(w io.Writer) error {
		_, err := w.Write([]byte("x"))
		return err
	}); err == nil {
		t.Fatal("replacing a non-empty directory reported success")
	}
	leftTemps(t, dir)
}

// And the same on a directory that is not there: the caller decides whether to
// make it, so nothing is created on the way.
func TestWriteStreamToAMissingDirectoryFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	called := false
	err := WriteStream(filepath.Join(dir, "gone", "sidecar.bin"), 0o600, func(_ io.Writer) error {
		called = true
		return nil
	})
	if err == nil {
		t.Error("writing into a missing directory reported success")
	}
	if called {
		t.Error("the writer ran even though there was nowhere to write")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		t.Errorf("the failed write left %d entries behind", len(entries))
	}
}

// leftTemps fails if this package left one of its temp files behind.
func leftTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("left %s behind", e.Name())
		}
	}
}

// The refusal publish retries is the rename's own access-denied or sharing
// violation, and nothing else: not another syscall's, not another error.
func TestOnlyARefusedRenameCountsAsOpen(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.Errno(5)}, true},
		{&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.Errno(32)}, true},
		{&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.Errno(2)}, false},
		{&os.LinkError{Op: "link", Old: "a", New: "b", Err: syscall.Errno(5)}, false},
		{&os.PathError{Op: "open", Path: "a", Err: syscall.Errno(5)}, false},
		{errors.New("access is denied"), false},
	} {
		if got := isOpenRefusal(c.err); got != c.want {
			t.Errorf("isOpenRefusal(%v) = %v, want %v", c.err, got, c.want)
		}
	}
	if runtime.GOOS != "windows" && refusedWhileOpen(&os.LinkError{Op: "rename", Err: syscall.Errno(5)}) {
		t.Error("a unix rename failure was treated as a windows refusal and would be retried")
	}
}

// A refusal that never clears runs the whole schedule and then reports the
// rename's error, leaving the old file and no temp behind.
func TestARefusalThatNeverClearsGivesUp(t *testing.T) {
	retryable = func(error) bool { return true }
	t.Cleanup(func() { retryable = refusedWhileOpen })
	dir := t.TempDir()
	path := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := Write(path, []byte("x"), 0o600); err == nil {
		t.Fatal("replacing a non-empty directory reported success")
	}
	if took := time.Since(start); took < time.Second {
		t.Errorf("gave up after %v; the schedule is meant to outlast a reader's grip", took)
	}
	leftTemps(t, dir)
}
