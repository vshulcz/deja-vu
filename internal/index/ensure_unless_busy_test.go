package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// EnsureUnlessBusy skips the wait only when there is an index to answer from.
// With one on disk and the lock held it says busy at once; with none yet it
// waits for the pass that is building it, as Ensure does, because answering
// from nothing would print an empty listing over a store full of sessions.
func TestEnsureUnlessBusyWaitsOnlyWhenThereIsNothingToRead(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"busy1","timestamp":"2026-01-02T03:04:05Z",` +
		`"message":{"role":"user","content":"the quetzalcoatl migration"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "busy1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	built := filepath.Join(tmp, "built.db")
	if err := Ensure(built, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if m, err := readManifest(built); err != nil || len(m.Sessions) != 1 || m.RecordsSize <= 0 {
		t.Fatalf("the seed did not index one session with records: %d sessions, %d record bytes (%v)", len(m.Sessions), m.RecordsSize, err)
	}
	unlock, err := lockDir(built)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		busy, ensureErr := EnsureUnlessBusy(built, nil)
		if ensureErr != nil {
			t.Errorf("held lock over a built index: %v", ensureErr)
		}
		done <- busy
	}()
	select {
	case busy := <-done:
		if !busy {
			t.Error("a held lock over a built index was not reported busy")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waited for the lock with an index on disk to answer from")
	}
	unlock()

	empty := filepath.Join(tmp, "empty.db")
	unlock, err = lockDir(empty)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		busy, ensureErr := EnsureUnlessBusy(empty, nil)
		if ensureErr != nil {
			t.Errorf("first build: %v", ensureErr)
		}
		done <- busy
	}()
	select {
	case <-done:
		unlock()
		t.Fatal("returned with no index on disk while another deja was building one")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case busy := <-done:
		if busy {
			t.Error("reported busy after waiting for the first build")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not finish after the lock was released")
	}
	if !ReadableSnapshot(empty) {
		t.Error("the wait did not end in a built index")
	}

	// A record log shorter than the manifest says: the sessions are listed but
	// their messages are gone, so `show` would print an empty session.
	if err = os.Truncate(filepath.Join(built, "records.bin"), 0); err != nil {
		t.Fatal(err)
	}
	unlock, err = lockDir(built)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		busy, ensureErr := EnsureUnlessBusy(built, nil)
		if ensureErr != nil {
			t.Errorf("torn record log: %v", ensureErr)
		}
		done <- busy
	}()
	select {
	case <-done:
		unlock()
		t.Fatal("answered from a torn record log instead of waiting for the rebuild")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case busy := <-done:
		if busy {
			t.Error("reported busy after waiting out the torn record log")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not finish after the lock was released")
	}
	m, err := readManifest(built)
	if err != nil || !recordsIntact(built, m) {
		t.Errorf("the wait did not end in an intact record log: %v", err)
	}
}
