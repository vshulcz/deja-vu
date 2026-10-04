package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func opencodeDBHome(t *testing.T) (home, xdg string) {
	t.Helper()
	home, xdg = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("XDG_DATA_HOME", xdg)
	return home, xdg
}

func touchDB(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// opencode takes its data directory from xdg-basedir, which reads
// XDG_DATA_HOME on every OS. deja read it on Linux only, so a mac with the
// variable set indexed nothing from opencode and its compaction packet came
// back empty.
func TestOpencodeDBFollowsXDGDataHomeOnEveryOS(t *testing.T) {
	_, xdg := opencodeDBHome(t)
	want := filepath.Join(xdg, "opencode", "opencode.db")
	touchDB(t, want)
	if got := OpencodeDB(); got != want {
		t.Fatalf("OpencodeDB() = %q, want %q", got, want)
	}
	if got, w := OpencodeDiffDir(), filepath.Join(xdg, "opencode", "storage", "session_diff"); got != w {
		t.Fatalf("OpencodeDiffDir() = %q, want %q", got, w)
	}
}

// opencode started from a GUI without the shell's XDG_DATA_HOME keeps its
// store in the default directory; that store is still read.
func TestOpencodeDBFallsBackWhenTheXDGStoreIsMissing(t *testing.T) {
	home, xdg := opencodeDBHome(t)
	def := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	touchDB(t, def)
	if got := OpencodeDB(); got != def {
		t.Fatalf("OpencodeDB() = %q, want the default store %q", got, def)
	}
	// With neither present the XDG path is the answer, as opencode would
	// create it there.
	if err := os.Remove(def); err != nil {
		t.Fatal(err)
	}
	if got, want := OpencodeDB(), filepath.Join(xdg, "opencode", "opencode.db"); got != want {
		t.Fatalf("OpencodeDB() = %q, want %q", got, want)
	}
}

func TestOpencodeDBHonoursOpencodeDBVariable(t *testing.T) {
	_, xdg := opencodeDBHome(t)
	abs := filepath.Join(t.TempDir(), "elsewhere.db")
	t.Setenv("OPENCODE_DB", abs)
	if got := OpencodeDB(); got != abs {
		t.Fatalf("absolute OPENCODE_DB: got %q, want %q", got, abs)
	}
	t.Setenv("OPENCODE_DB", "custom.db")
	if got, want := OpencodeDB(), filepath.Join(xdg, "opencode", "custom.db"); got != want {
		t.Fatalf("relative OPENCODE_DB: got %q, want %q", got, want)
	}
	t.Setenv("DEJA_OPENCODE_DB", "/pinned.db")
	if got := OpencodeDB(); got != "/pinned.db" {
		t.Fatalf("DEJA_OPENCODE_DB must win, got %q", got)
	}
}
