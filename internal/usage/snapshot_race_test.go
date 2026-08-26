package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// A digest is kilobytes, not the ~100 bytes a usage event is, and the injection
// log takes the whole record in one Write on an append descriptor with no lock.
// The question is whether two agents starting together can interleave two of
// them into a line that parses as neither — the shape #1319 was about, and the
// one the reader's own filtering would hide rather than report.
//
// They cannot here: one write of a whole record reaches the file whole, on the
// systems this runs on. Not a guarantee — os.File.Write loops on short writes,
// and a network filesystem promises nothing about an append — which is why this
// is measured rather than assumed.
//
// Sizes stay under the rotation threshold on purpose: past it, a rotation
// rewrites the file from the lines the reader accepts and would delete the torn
// line before the assertion ever saw it (#1971).
func TestConcurrentWritersNeverInterleaveASnapshot(t *testing.T) {
	for _, size := range []int{200, 4096, 64 << 10} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			dir := t.TempDir()
			// writers × each × size stays well under snapshotRotateAt, so no
			// rotation can delete the evidence.
			writers, each := 16, 8
			switch {
			case size > 4096:
				writers, each = 4, 2
			case size > 200:
				writers, each = 8, 4
			}
			var wg sync.WaitGroup
			for w := 0; w < writers; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					body := strings.Repeat(string(rune('a'+w)), size)
					for i := 0; i < each; i++ {
						RecordDigestPolicy(dir, KindHook, body, 1, 0, "local-only")
					}
				}(w)
			}
			wg.Wait()

			f, err := os.Open(SnapshotPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
			lines := 0
			for sc.Scan() {
				lines++
				var s Snapshot
				if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
					t.Fatalf("line %d holds no whole record: %v", lines, err)
				}
				// One writer's letter, repeated, and nothing else: a record
				// carrying part of another's would still parse.
				if len(s.Digest) != size || strings.Count(s.Digest, s.Digest[:1]) != size {
					t.Fatalf("line %d holds %d bytes of mixed digest", lines, len(s.Digest))
				}
			}
			if err := sc.Err(); err != nil {
				t.Fatal(err)
			}
			if lines != writers*each {
				t.Fatalf("file holds %d lines, wrote %d — a rotation would hide a torn line by deleting it", lines, writers*each)
			}
		})
	}
}

// Above the cliff every write rotates, and every record appended while a
// rotation rebuilds is rewritten away. Twenty records of 26 kB or more exceed
// the 512 kB threshold, so the file never drops back under it — and a whole
// session read through `deja://session/…` is recorded here unbudgeted, which is
// how a real store gets there (#1971).
func TestARotationLeavesTheFileUnderItsOwnThreshold(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("y", 40<<10)
	for i := 0; i < snapshotsToKeep+4; i++ {
		RecordDigestPolicy(dir, KindHook, big, 1, 0, "local-only")
	}
	fi, err := os.Stat(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() >= snapshotRotateAt {
		t.Errorf("the log is %d bytes after a rotation, over its own %d threshold: every write from here rotates",
			fi.Size(), snapshotRotateAt)
	}
}

// And the newest digest survives the rotation that shrinks the file, which is
// the record `deja log --last` exists to show.
func TestTheNewestDigestSurvivesARotationOfBigRecords(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("y", 40<<10)
	for i := 0; i < snapshotsToKeep+4; i++ {
		RecordDigestPolicy(dir, KindHook, big, 1, 0, "local-only")
	}
	RecordDigestPolicy(dir, KindHook, "the one just served", 1, 0, "local-only")

	got := Snapshots(dir, 0)
	if len(got) == 0 {
		t.Fatal("the log is empty")
	}
	if got[0].Digest != "the one just served" {
		t.Errorf("newest digest is %d bytes of the wrong record", len(got[0].Digest))
	}
}
