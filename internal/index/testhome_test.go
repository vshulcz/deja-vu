package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// setHome points a test's home at dir the way every platform reads it.
//
// t.Setenv("HOME", …) alone is a no-op on windows: sources.Home() goes through
// os.UserHomeDir(), which reads USERPROFILE there, and the notes file goes
// through os.UserConfigDir(), which reads APPDATA. TestMain sets both once for
// the whole package, so on windows a test that thought it had its own home was
// reading and writing the package's — one test's note showed up as a leaked
// `deja` session in the next test's index, which is what a dozen tests in here
// were skipped for.
//
// The unix behaviour is unchanged: HOME still decides, and the extra keys are
// only read on windows.
//
// XDG_CONFIG_HOME follows the home too. TestMain sets it once for the package,
// and it outranks HOME, so every tombstone and exclude list a test wrote went
// to the one directory all tests share: the next test to index a session of
// the same id found it already forgotten (#5570).
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("USERPROFILE", dir)
	t.Setenv("APPDATA", filepath.Join(dir, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "AppData", "Local"))
}

// A tombstone one test writes must not be in the next test's config (#5570).
func TestSetHomeGivesTheTestItsOwnConfig(t *testing.T) {
	dir := t.TempDir()
	setHome(t, dir)
	if got, want := sources.ConfigDir(), filepath.Join(dir, ".config", "deja"); got != want {
		t.Fatalf("config dir after setHome = %s, want %s under the test's home", got, want)
	}
}

// The next test written in here will be copied from the one above it, and the
// line being copied used to be t.Setenv("HOME", …). That reads as isolation on
// the machine it was written on and provides none on windows, which is a thing
// nobody discovers until a test that looks unrelated starts failing there.
func TestTestsInThisPackageSetHomeThroughTheHelper(t *testing.T) {
	names, err := filepath.Glob(filepath.Join(".", "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 2 {
		t.Fatalf("globbed %d test files; the guard is reading the wrong directory", len(names))
	}
	for _, name := range names {
		if name == filepath.Join(".", "testhome_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, `t.Setenv("HOME"`) {
				t.Errorf("%s:%d sets HOME directly; use setHome, which windows also reads", name, i+1)
			}
		}
	}
}
