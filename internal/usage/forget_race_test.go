package usage

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The injection log is append-only so agents can write to it at once. A forget
// sweep reads the whole file, drops what matched and writes the rest back, and
// anything appended in between was written over — the record of an injection an
// agent had already received, gone with no sign (#2413).
func TestAnInjectionAppendedDuringAForgetSurvivesIt(t *testing.T) {
	for round := 0; round < 3; round++ {
		dir := filepath.Join(t.TempDir(), "index.db")
		for i := 0; i < 300; i++ {
			RecordDigestInto(dir, "hook", fmt.Sprintf("digest for gone %d", i), "gone-"+fmt.Sprint(i), 1, 100, []string{"t"})
		}
		const appended = 200
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < appended; i++ {
				RecordDigestInto(dir, "hook", fmt.Sprintf("digest for keep %d", i), "keep-"+fmt.Sprint(i), 1, 100, []string{"t"})
			}
		}()
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
