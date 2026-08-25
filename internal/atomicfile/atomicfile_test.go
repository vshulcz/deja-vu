package atomicfile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The point of the package: several writers at once, and a reader that only
// ever sees one of their payloads whole.
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
	for w := range payloads {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 500 && !stop.Load(); i++ {
				if err := Write(path, payloads[w], 0o600); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	var reads, mixed int64
	wg.Add(1)
	go func() {
		defer wg.Done()
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
	}()
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
			end := off + 512
			if end > len(body) {
				end = len(body)
			}
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
	err := WriteStream(filepath.Join(dir, "gone", "sidecar.bin"), 0o600, func(w io.Writer) error {
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
