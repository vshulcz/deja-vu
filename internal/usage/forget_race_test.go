package usage

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The injection log is append-only so agents can write to it at once. A forget
// sweep reads the whole file, drops what matched and writes the rest back, and
// anything appended in between was written over — the record of an injection an
// agent had already received, gone with no sign (#2413).
func TestAnInjectionAppendedDuringAForgetSurvivesIt(t *testing.T) {
	for round := range 3 {
		dir := filepath.Join(t.TempDir(), "index.db")
		for i := range 300 {
			RecordDigestInto(dir, "hook", fmt.Sprintf("digest for gone %d", i), "gone-"+strconv.Itoa(i), 1, 100, []string{"t"})
		}
		const appended = 200
		var wg sync.WaitGroup
		wg.Go(func() {
			for i := range appended {
				RecordDigestInto(dir, "hook", fmt.Sprintf("digest for keep %d", i), "keep-"+strconv.Itoa(i), 1, 100, []string{"t"})
			}
		})
		gone, err := ForgetSnapshots(dir, func(s Snapshot) bool {
			return strings.Contains(s.Digest, "gone")
		})
		wg.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if gone == 0 {
			t.Fatalf("round %d: the sweep matched nothing, so it never rewrote the file", round)
		}
		kept := 0
		for _, s := range Snapshots(dir, 0) {
			if strings.Contains(s.Digest, "keep") {
				kept++
			}
		}
		if kept != appended {
			t.Errorf("round %d: %d of %d injections were written over by the sweep", round, appended-kept, appended)
		}
	}
}
