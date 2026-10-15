package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The mirror beside the index was written only by forget, and every pass that
// writes a new index deleted it with the old one. With the config copy gone
// too, the second rebuild after a forget brought the session back (#5052).
func TestForgetSurvivesRebuildsWithoutTheConfigDirectory(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	write(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "session.jsonl"),
		claudeLine("sess-1", "2026-01-02T03:04:05Z", "forgotten needle"))
	dir := DefaultDir()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if r, err := Forget(dir, ForgetOptions{Session: "sess-1"}); err != nil || r.Sessions != 1 {
		t.Fatalf("forget=%#v err=%v", r, err)
	}
	if err := os.RemoveAll(filepath.Join(tmp, "home", ".config")); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(tombstoneMirrorPath(dir)); err != nil {
			t.Fatalf("rebuild %d dropped the tombstone mirror: %v", i+1, err)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		for key := range m.Sessions {
			t.Fatalf("rebuild %d brought back %s", i+1, key)
		}
	}
}

// A mirror lost earlier is written again from the config copy by the next
// swap, so either copy alone keeps the pair in force.
func TestSwapWritesTheMirrorFromTheConfigCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	dir := filepath.Join(home, "idx")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := appendTombstones([]string{"claude:gone"}); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(home, "next")
	if err := os.MkdirAll(next, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, next); err != nil {
		t.Fatal(err)
	}
	if !readTombstoneFile(tombstoneMirrorPath(dir))["claude:gone"] {
		t.Fatal("the swapped-in index has no mirror of the config copy")
	}
}

// Hook text waiting for its session lives in <index>/deferred, which no build
// writes, so a rebuild between the hook that could not speak and the one that
// could deleted it (#5052).
func TestSwapKeepsDeferredHookText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	dir := filepath.Join(home, "idx")
	waiting := filepath.Join(dir, "deferred", "abc", "0start")
	if err := os.MkdirAll(filepath.Dir(waiting), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(waiting, []byte("digest"), 0o600); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(home, "next")
	if err := os.MkdirAll(next, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, next); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(waiting); err != nil || string(b) != "digest" {
		t.Fatalf("deferred text after the swap: %q, %v", b, err)
	}
}
