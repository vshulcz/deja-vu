package index

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
