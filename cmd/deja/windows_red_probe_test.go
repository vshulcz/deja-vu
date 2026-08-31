package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A measurement, not a guard. The Windows leg has been red on main for 35
// tests across six areas (#2808), and the whole set has never been looked at
// from inside the runner. This reports what each family's decisive value
// actually is there. It fails on purpose — a passing test prints nothing — and
// does not belong on a long-lived branch.
func TestWindowsRedProbe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the probe is about Windows")
	}
	// Exactly the statusline tests' own setup: they pin the index directory
	// and nothing else, so what they see is the runner's own home — which is
	// what a hermetic probe cannot show.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))

	// The statusline family: five tests, all reporting "the index cannot
	// answer", which needs indexNeedsRebuild and history to be true together.
	var stores []string
	for _, check := range doctorStoreChecks() {
		for _, f := range check.files {
			if fi, err := os.Stat(f); err == nil && fi.Size() > 0 {
				stores = append(stores, check.name+":"+f)
			}
		}
	}
	t.Errorf("probe statusline: needsRebuild=%v history=%v stores=%v",
		indexNeedsRebuild(index.DefaultDir()), !noAgentHistoryFound(), stores)

	// Every store the history check walks, with how many files it found in
	// each: the hermetic run said "no history", so the answer is in the
	// runner's own home.
	var walked []string
	for _, check := range doctorStoreChecks() {
		walked = append(walked, fmt.Sprintf("%s=%d", check.name, len(check.files)))
	}
	t.Errorf("probe stores: %v", walked)

	// And what the install family derives, since those paths are the other
	// thing that differs here.
	t.Errorf("probe install: home=%q appdata=%q openclaw=%q zed=%q",
		sources.Home(), os.Getenv("APPDATA"), sources.OpenClawStateDir(), sources.ZedSettingsPath())

	// The ignore-rule family: eight tests, all about what a rule withholds.
	// The rule is a path pattern, and paths are what differ here.
	t.Errorf("probe paths: home=%q sep=%q index=%q notes=%q",
		sources.Home(), string(filepath.Separator), index.DefaultDir(), sources.NotesFile())
}
