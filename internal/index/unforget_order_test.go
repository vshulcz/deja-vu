package index

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func contains(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// Interrupted the other way round, the tombstone that would let a retry work
// was already gone while the index had not been rebuilt yet, and nothing on
// the machine could say the session was missing (#810).
func TestUnforgetKeepsTheTombstoneUntilTheRebuildSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p", "s.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "manneedle here"))
	o := search.Options{Query: "manneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	if !contains(Tombstones(), "claude:s1") {
		t.Fatalf("tombstones = %v, want the forgotten session", Tombstones())
	}

	// A rebuild that cannot finish. The parent, not the index directory
	// itself: a rebuild recreates that directory, so locking it changes
	// nothing.
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	restore := func() { _ = os.Chmod(parent, 0o700) }
	defer restore()
	if _, err := Unforget(dir, "claude:s1", nil); err == nil {
		t.Fatal("unforget reported success with an unwritable index")
	}
	if !contains(Tombstones(), "claude:s1") {
		t.Errorf("tombstones = %v after a failed rebuild, want the session still forgotten", Tombstones())
	}
	restore()

	// And the retry works, which is the whole point of keeping it.
	lifted, err := Unforget(dir, "claude:s1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lifted != 1 {
		t.Errorf("lifted = %d, want 1", lifted)
	}
	if contains(Tombstones(), "claude:s1") {
		t.Errorf("tombstones = %v after a successful unforget, want it gone", Tombstones())
	}
	hits, err := Search(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("the restored session is not searchable")
	}
}
