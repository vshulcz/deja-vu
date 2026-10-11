package index

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

// checkRecordOffsets reads every session of dir through the offset table,
// one session at a time and forced past the walk-share cut, and fails when
// that does not give exactly the records a walk of the log gives for it.
func checkRecordOffsets(t *testing.T, dir string, m Manifest, recs []OffsetRecord) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, recordOffsetsFile)); err != nil {
		return
	}
	defer func(share int64) { offsetsWalkShare = share }(offsetsWalkShare)
	offsetsWalkShare = 1
	walked := map[string][]string{}
	for _, r := range recs {
		walked[r.Record.Key] = append(walked[r.Record.Key], fmt.Sprintf("%s|%q", r.Record.Role, r.Record.Text))
	}
	tb := tablesFromManifest(m)
	for key := range m.Sessions {
		before := atomic.LoadInt64(&recordOffsetReads)
		var got []string
		err := eachRecordForKeys(filepath.Join(dir, "records.bin"), tb, map[string]bool{key: true}, func(r Record) {
			got = append(got, fmt.Sprintf("%s|%q", r.Role, r.Text))
		})
		if err != nil {
			t.Fatalf("offset read of %s: %v", key, err)
		}
		if atomic.LoadInt64(&recordOffsetReads) == before {
			t.Errorf("%s: the offset table did not answer", key)
		}
		if !slices.Equal(got, walked[key]) {
			t.Errorf("%s: the offset table reads %d records, the walk %d:\n  table %q\n  walk  %q", key, len(got), len(walked[key]), got, walked[key])
		}
	}
}
