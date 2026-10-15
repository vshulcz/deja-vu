package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const aiderZoneHistory = "# aider chat started at 2026-07-20 23:45:00\n\n#### why does the pool run dry  \n\nthe cap is too low\n\n" +
	"# aider chat started at 2026-07-21 00:30:00\n\n#### raise the pool cap to 40  \n\nraised it\n"

func aiderZoneEnv(t *testing.T) (hist, dir string) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_AIDER_ROOTS": "aider"})
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	hist = filepath.Join(tmp, "aider", "proj", ".aider.chat.history.md")
	parityWrite(t, hist, aiderZoneHistory, false)
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	return hist, filepath.Join(tmp, "index.db")
}

// aider headers are read in the machine's zone, so an index built before a
// zone change held other instants than a rebuild after it, and an incremental
// pass kept them (#5053). The pass after the change rereads them.
func TestAiderZoneChangeMatchesRebuild(t *testing.T) {
	hist, dir := aiderZoneEnv(t)
	time.Local = time.FixedZone("pago", -11*3600)
	parityPass(t, dir, false)
	if AiderZoneDrift(dir) {
		t.Fatal("an index read in this zone reports drift")
	}

	time.Local = time.FixedZone("kiritimati", 14*3600)
	if !AiderZoneDrift(dir) {
		t.Error("an index read 25 hours east reports no drift")
	}
	parityPass(t, dir, false)
	if AiderZoneDrift(dir) {
		t.Error("drift is still reported after the pass")
	}
	sameAsRebuild(t, dir)

	// Growth after the change is read in the new zone and stays in step.
	parityWrite(t, hist, "# aider chat started at 2026-07-22 08:00:00\n\n#### add a pool metric  \n\nadded it\n", true)
	parityPass(t, dir, false)
	sameAsRebuild(t, dir)
}

// A kept row of a deleted history stays as it was read, by a rebuild too.
// Counting it as drift would rebuild the index on every pass.
func TestAiderKeptRowsAreNotZoneDrift(t *testing.T) {
	hist, dir := aiderZoneEnv(t)
	time.Local = time.FixedZone("pago", -11*3600)
	parityPass(t, dir, false)
	if err := os.Remove(hist); err != nil {
		t.Fatal(err)
	}
	parityPass(t, dir, false)
	// aider starts a new history at the same path (#4332).
	parityWrite(t, hist, "# aider chat started at 2026-08-01 09:00:00\n\n#### rename the loader  \n\nrenamed it\n", false)
	parityPass(t, dir, false)

	time.Local = time.FixedZone("kiritimati", 14*3600)
	parityPass(t, dir, false)
	if AiderZoneDrift(dir) {
		t.Error("kept rows of the deleted history count as drift after the pass")
	}
	// A fresh build has no kept rows, so the rebuild is in place.
	before := paritySnapshot(t, dir)
	parityPass(t, dir, true)
	if after := paritySnapshot(t, dir); fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("incremental index differs from a rebuild:\n  incremental: %v\n  rebuild:     %v", before, after)
	}
	if AiderZoneDrift(dir) {
		t.Error("kept rows count as drift after a rebuild")
	}
}
