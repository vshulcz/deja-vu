package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config directory deja cannot write to is ordinary: a managed machine, a
// directory owned by another account, a mount gone read-only. Install has to
// say so and leave the file as it was. Silently reporting success is the worse
// half — the user believes memory is wired and it is not.
func TestInstallReportsAReadOnlyConfigDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
	for _, tc := range []struct{ target, rel string }{
		{"qwen-auto", ".qwen/settings.json"},
		{"codex-auto", ".codex/hooks.json"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			const theirs = "{\"theirs\":true}\n"
			if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Skipf("cannot make the directory read-only: %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
			// Windows ignores the permission bits on a directory, so ask the
			// filesystem whether the change took rather than assuming it.
			probe := filepath.Join(dir, "deja-write-probe")
			if err := os.WriteFile(probe, []byte("x"), 0o644); err == nil {
				_ = os.Remove(probe)
				t.Skip("this filesystem still allows writes into a read-only directory")
			}

			_, err := installTarget(tc.target, "/usr/local/bin/deja", false)
			after, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("their config is gone: %v", rerr)
			}
			if string(after) != theirs {
				t.Fatalf("their config changed under a read-only directory:\n%s", after)
			}
			if err == nil {
				t.Error("install reported success while unable to write")
				return
			}
			// The message has to name the path, or the reader has nothing to
			// act on.
			if !strings.Contains(err.Error(), filepath.Base(path)) &&
				!strings.Contains(err.Error(), dir) {
				t.Errorf("the error does not say which file it could not write: %v", err)
			}
		})
	}
}
