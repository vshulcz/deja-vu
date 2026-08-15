package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The Windows IDE root is %APPDATA%\Cursor\User. Before this branch existed,
// CursorUserRoot fell through to ~/.config/Cursor/User — a directory Cursor
// never writes on Windows — so a machine with megabytes of chats in
// state.vscdb reported no IDE stores at all. Runs natively on any Windows
// host, the CI leg included.
func TestCursorUserRootWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("resolution under test is the windows branch")
	}
	t.Setenv("DEJA_CURSOR_ROOT", "")

	appdata := filepath.Join(t.TempDir(), "Roaming")
	if err := os.MkdirAll(appdata, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", appdata)
	if got, want := CursorUserRoot(), filepath.Join(appdata, "Cursor", "User"); got != want {
		t.Fatalf("CursorUserRoot with APPDATA = %q, want %q", got, want)
	}

	// XDG must not leak into the Windows resolution the way it did before:
	// pointing it at an existing dir changes nothing here.
	xdg := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "Cursor", "User"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := CursorUserRoot(), filepath.Join(appdata, "Cursor", "User"); got != want {
		t.Fatalf("CursorUserRoot ignores XDG on windows, got %q want %q", got, want)
	}

	// A stripped environment still lands on the real location.
	t.Setenv("APPDATA", "")
	if got, want := CursorUserRoot(), filepath.Join(Home(), "AppData", "Roaming", "Cursor", "User"); got != want {
		t.Fatalf("CursorUserRoot without APPDATA = %q, want %q", got, want)
	}
}
