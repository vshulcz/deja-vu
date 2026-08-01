package index

import (
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Every command reads the elided id a result line prints (#853) — forget was
// the last one that did not, answering "nothing was changed" about a session
// that is there (#855). Of all the commands to silently match nothing, this is
// the worst one.
func TestForgetAndUnforgetTakeAnElidedId(t *testing.T) {
	root, dir := allHarnessEnv(t)
	long := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"
	write(t, filepath.Join(root, "claude", "-tmp-p", "s.jsonl"),
		claudeLine(long, "2026-01-02T03:04:05Z", "manneedle here"))
	if err := EnsureForSearch(dir, search.Options{Query: "manneedle", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}

	// Exactly what short() prints for a 40-character id.
	elided := "a1b2c3d4e…d6e7f8a9b0"
	dry, err := Forget(dir, ForgetOptions{Session: elided, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Sessions != 1 {
		t.Errorf("dry run matched %d sessions with the id from the screen, want 1", dry.Sessions)
	}

	// An elision that belongs to no session still matches nothing.
	if miss, err := Forget(dir, ForgetOptions{Session: "zzzz…zzzz", DryRun: true}); err != nil {
		t.Fatal(err)
	} else if miss.Sessions != 0 {
		t.Errorf("an elision matching nothing dropped %d sessions", miss.Sessions)
	}

	// And the round trip: forget by the printed id, restore by the same string.
	if got, err := Forget(dir, ForgetOptions{Session: elided}); err != nil {
		t.Fatal(err)
	} else if got.Sessions != 1 {
		t.Fatalf("forget matched %d sessions", got.Sessions)
	}
	lifted, err := Unforget(dir, elided, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lifted != 1 {
		t.Errorf("unforget lifted %d tombstones with the id from the screen, want 1", lifted)
	}
	// Other tests in this package share the tombstone file; what matters is
	// that this session is not among what is left.
	for _, key := range Tombstones() {
		if key == "claude:"+long {
			t.Errorf("the restored session is still tombstoned: %v", Tombstones())
		}
	}
}
