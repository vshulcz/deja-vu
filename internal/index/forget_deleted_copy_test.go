package index

import (
	"os"
	"path/filepath"
	"testing"
)

// A session with a copy in another project that was deleted, and so kept,
// then forgotten: the pass kept the deleted copy's file marked kept, a
// rebuild of the same index did not (#5521).
func TestForgetDropsTheKeptFileOfADeletedCopy(t *testing.T) {
	h := newTwoWayEnv(t)
	a := filepath.Join(h.tmp, "a.db")
	// Tombstones live under XDG_CONFIG_HOME, which the suite shares: an
	// earlier test that forgot an s3 would leave this one nothing to forget.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h.tmp, "config"))
	pass := func() {
		t.Helper()
		if err := Ensure(a, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	h.put("app", "s1", hPrompt("app", "s1", 2, 0, "why does the pool fail"))
	h.put("app", "s3", hPrompt("app", "s3", 3, 0, "why does the queue fail"))
	pass()
	h.put("fork", "s3", hPrompt("fork", "s3", 3, 0, "copy: why does the queue fail")+
		hBash("fork", "s3", 3, 1, "c1", "make migrate", "ok", false))
	pass()
	if err := os.Remove(h.path("fork", "s3")); err != nil {
		t.Fatal(err)
	}
	pass()
	if _, err := Forget(a, ForgetOptions{Session: "s3"}); err != nil {
		t.Fatal(err)
	}
	pass()
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	if err := Ensure(b, "", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, "forget after a deleted copy", takeSnap(t, a), takeSnap(t, b))
}
