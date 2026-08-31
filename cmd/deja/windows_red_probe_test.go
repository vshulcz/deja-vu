package main

import (
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
	tmp := hermeticEnv(t)

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

	// The install family: what a writer sees for a config it is about to
	// touch, and whether the paths it derives are where the test put them.
	t.Errorf("probe install: home=%q claude=%q appdata=%q openclaw=%q zed=%q",
		sources.Home(), os.Getenv("DEJA_CLAUDE_ROOT"), os.Getenv("APPDATA"),
		sources.OpenClawStateDir(), sources.ZedSettingsPath())

	// The ignore-rule family: eight tests, all about what a rule withholds.
	// The rule is a path pattern, and paths are what differ here.
	t.Errorf("probe paths: tmp=%q sep=%q index=%q notes=%q",
		tmp, string(filepath.Separator), index.DefaultDir(), sources.NotesFile())
}
