package index

import (
	"fmt"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session records what it worked on so the line before an edit can say how
// many sessions touched this file. Six was enough to describe a session and too
// few to answer that question: measured over 334 files an agent really edited,
// 257 had been touched by no session at all as far as the index knew, while 268
// were spoken about in sessions the store holds.
func TestASessionRecordsTheFilesItReallyWorkedOn(t *testing.T) {
	var ms []model.Message
	for i := 0; i < 30; i++ {
		// Recurrence decides the order, so give the later files fewer touches:
		// what is pinned is that the tail is kept at all, not that it is ranked
		// differently than before.
		for r := 0; r < 30-i; r++ {
			ms = append(ms, model.Message{
				Role: roleFiles,
				Text: fmt.Sprintf("internal/index/file%02d.go", i),
			})
		}
	}
	got, hits := topTouchedCounted(ms)
	if len(got) < 20 {
		t.Errorf("a session that worked on 30 files recorded %d of them", len(got))
	}
	if len(got) > touchedFileCap {
		t.Errorf("recorded %d files, the cap is %d", len(got), touchedFileCap)
	}
	if len(hits) != len(got) {
		t.Errorf("%d paths carry %d counts", len(got), len(hits))
	}
	// The most-touched file still leads: the cap decides how long the tail is,
	// not what the head is.
	if got[0] != "internal/index/file00.go" {
		t.Errorf("the file worked on most is not first: %q", got[0])
	}
}

// And the bound still bounds: a session that touched hundreds does not carry
// hundreds into every hook that reads the manifest.
func TestTheRecordedListIsStillBounded(t *testing.T) {
	var ms []model.Message
	for i := 0; i < 500; i++ {
		ms = append(ms, model.Message{Role: roleFiles, Text: fmt.Sprintf("pkg/f%03d.go", i)})
	}
	got, _ := topTouchedCounted(ms)
	if len(got) != touchedFileCap {
		t.Errorf("recorded %d files of 500, want the cap of %d", len(got), touchedFileCap)
	}
}
