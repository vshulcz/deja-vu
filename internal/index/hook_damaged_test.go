package index

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHookDamagedVsDamaged(t *testing.T) {
	dir, proj := swapFixture(t)
	writeSession(t, proj, "b", "mercurial dandelion telescope counterweight")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if HookDamaged(dir) {
		t.Fatal("freshly built index reported HookDamaged")
	}
	if Damaged(dir) {
		t.Fatalf("freshly built index reported Damaged: %s", DamageReason(dir))
	}

	bucketsDir := filepath.Join(dir, "buckets")
	entries, err := os.ReadDir(bucketsDir)
	if err != nil {
		t.Fatal(err)
	}
	postings := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && isPostingsFile(e.Name()) {
			postings = append(postings, filepath.Join(bucketsDir, e.Name()))
		}
	}
	if len(postings) < 2 {
		t.Fatalf("expected at least 2 postings files in %s, got %d", bucketsDir, len(postings))
	}

	// Removing one bucket file triggers full Damaged() while HookDamaged() stays
	// fast and accepts a non-empty postings directory.
	if err := os.Remove(postings[0]); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	if HookDamaged(dir) {
		t.Error("HookDamaged returned true when postings files remain")
	}
	if !Damaged(dir) {
		t.Error("Damaged returned false after removing a bucket file")
	}

	// Removing all remaining postings files makes HookDamaged report true.
	for _, p := range postings[1:] {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	invalidateManifestCache(dir)
	if !HookDamaged(dir) {
		t.Error("HookDamaged returned false when buckets directory is empty")
	}

	// Rebuilding restores both checks, and truncating records.bin trips HookDamaged.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	recordsPath := filepath.Join(dir, "records.bin")
	fi, err := os.Stat(recordsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(recordsPath, fi.Size()/2); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	if !HookDamaged(dir) {
		t.Error("HookDamaged returned false when records.bin is truncated")
	}
}

func TestWaitFreeExtendedRecordsWhileBuildLockHeld(t *testing.T) {
	dir, _ := swapFixture(t)

	waited := 0
	oldWait := waitOutSwapWindow
	waitOutSwapWindow = func() { waited++ }
	t.Cleanup(func() { waitOutSwapWindow = oldWait })

	unlock, ok, err := tryLockDir(dir)
	if err != nil || !ok {
		t.Fatalf("tryLockDir(%q) = (%v, %v), want lock acquired", dir, ok, err)
	}
	defer unlock()

	f, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("uncommitted-tail-record"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	invalidateManifestCache(dir)
	if _, err := readManifestCached(dir); err != nil {
		t.Fatal(err)
	}
	decodesBefore := manifestDecodes.Load()

	if HookDamaged(dir) {
		t.Error("HookDamaged returned true for extended records.bin under active build lock")
	}
	if reason := DamageReason(dir); reason != "" {
		t.Errorf("DamageReason = %q under active build lock, want empty", reason)
	}
	if waited != 0 {
		t.Errorf("waitOutSwapWindow called %d times, want 0", waited)
	}
	if got := manifestDecodes.Load() - decodesBefore; got != 0 {
		t.Errorf("extra uncached manifest decodes = %d, want 0", got)
	}
}

func TestRebuildInProgressIsReadOnly(t *testing.T) {
	parent := t.TempDir()
	absentDir := filepath.Join(parent, "absent-index.db")
	if RebuildInProgress(absentDir) {
		t.Fatal("RebuildInProgress returned true on absent index")
	}
	if _, err := os.Stat(absentDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("RebuildInProgress created %s: err=%v", absentDir, err)
	}
	if _, err := os.Stat(absentDir + ".lock"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("RebuildInProgress created %s: err=%v", absentDir+".lock", err)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	roDir := filepath.Join(parent, "ro-index.db")
	if err := os.MkdirAll(roDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(roDir+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(roDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o700) })

	if RebuildInProgress(roDir) {
		t.Fatal("RebuildInProgress returned true on unlocked directory")
	}
	fi, err := os.Stat(roDir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o500 {
		t.Fatalf("RebuildInProgress changed dir mode to %#o, want 0500", mode)
	}
}
