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
// They cannot: one write to a regular file is not split. Pinned across three
// sizes because the answer is about the size, and 64 kB is past any buffer this
// code has.
func TestConcurrentWritersNeverInterleaveASnapshot(t *testing.T) {
	for _, size := range []int{200, 4096, 64 << 10} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			dir := t.TempDir()
			const writers, each = 16, 8
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
			if lines == 0 {
				t.Fatal("nothing was written")
			}
		})
	}
}
