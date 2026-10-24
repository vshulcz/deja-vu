package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A refused target writes nothing, also when its config is not there yet and
// the directory it goes in cannot be made: cursor-auto wired mcp.json and
// hooks.json before finding out it could not create ~/.config/cursor (#5291).
// A regular file where the directory should be fails on every platform and
// for root, where a 0555 parent would not.
func TestCursorAutoRefusesAStatusLineDirectoryItCannotMakeBeforeWriting(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	cfg := cursorCLIConfigPath()
	blocker := filepath.Dir(cfg)
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// On unix the read of cli-config.json already fails with ENOTDIR and
	// refuses the target; Windows reports the path as not found, and the
	// pre-flight's mkdir check is what refuses it there. Either way nothing
	// may have been written.
	_, err := captureRun(t, "install", "cursor-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "cursor-auto") ||
		(!strings.Contains(err.Error(), "mkdir "+blocker) && !strings.Contains(err.Error(), "not a directory")) {
		t.Fatalf("install err = %v, want cursor-auto refused over %s", err, blocker)
	}
	for _, name := range []string{"mcp.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(sources.CursorCLIHome(), name)); err == nil {
			t.Errorf("%s was written by a refused target", name)
		}
	}

	// With the directory makeable, the same install goes through.
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "cursor-auto", "--no-index"); err != nil {
		t.Fatalf("install after removing the blocker: %v\n%s", err, out)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Errorf("status line file not written: %v", err)
	}
}

// The issue's own reproduction: ~/.config is there and read-only, so
// ~/.config/cursor cannot be made. Needs the mode bit to mean something.
func TestCursorAutoRefusesAReadOnlyConfigParentBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	hermeticEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	parent := filepath.Dir(filepath.Dir(cursorCLIConfigPath()))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	_, err := captureRun(t, "install", "cursor-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("install err = %v, want a permission refusal", err)
	}
	for _, name := range []string{"mcp.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(sources.CursorCLIHome(), name)); err == nil {
			t.Errorf("%s was written by a refused target", name)
		}
	}
}

func TestConfigWritableAsksWhetherAMissingConfigCanBeMade(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "have.json")
	if err := os.WriteFile(existing, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"existing file", existing, ""},
		{"missing file in an existing directory", filepath.Join(root, "new.json"), ""},
		{"missing directories under a writable one", filepath.Join(root, "a", "b", "new.json"), ""},
		{"a file where the directory should be", filepath.Join(blocker, "new.json"), "mkdir " + blocker},
		{"a file further up", filepath.Join(blocker, "sub", "new.json"), "mkdir " + blocker},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := configWritable(tc.path)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("configWritable(%s) = %v, want nil", tc.path, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("configWritable(%s) = %v, want %q", tc.path, err, tc.wantErr)
			}
		})
	}
	// The probe leaves nothing behind: no scratch file, no directory made.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n := e.Name(); n != "have.json" && n != "blocker" {
			t.Errorf("configWritable left %s behind", n)
		}
	}
}

func TestConfigWritableNamesTheDirectoryItCouldNotMake(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	err := configWritable(filepath.Join(locked, "cursor", "cli-config.json"))
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "mkdir "+filepath.Join(locked, "cursor")) {
		t.Errorf("missing directory: err = %v, want mkdir %s: permission denied", err, filepath.Join(locked, "cursor"))
	}
	path := filepath.Join(locked, "cli-config.json")
	err = configWritable(path)
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), path) {
		t.Errorf("missing file: err = %v, want a permission error naming %s", err, path)
	}
}
