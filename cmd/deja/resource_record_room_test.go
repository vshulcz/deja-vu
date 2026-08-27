package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `TestEveryAnswerFitsTheRoomTheLogHas` pins the budgets against the injection
// log's per-record room. It pins the constants; this reads a whole session
// through the resources door and measures the line that actually lands in the
// log — the header, the frame and the escaping the constant says nothing
// about. A record past that room makes the log rotate on every write, and half
// of two concurrent injections is then rewritten away (#1971).
func TestAWholeSessionReadAsAResourceFitsTheLogsRoom(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Far more session than any budget will serve, so what comes back is the
	// budget's doing rather than the fixture's.
	var lines []string
	for i := 0; i < 200; i++ {
		body, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": "big1", "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-time.Duration(200-i) * time.Minute).UTC().Format(time.RFC3339),
			"message": map[string]any{"role": "user", "content": fmt.Sprintf(
				"turn %d: the pgbouncer pool timed out while the migration held the lock, %s", i,
				strings.Repeat("and we went round again ", 40))},
		})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(body))
	}
	if err := os.WriteFile(filepath.Join(root, "big1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, code, msg := mcpResourceRead(dir, "deja://session/big1"); code != 0 {
		t.Fatalf("the resource could not be read: %d %s", code, msg)
	}

	snaps := usage.Snapshots(dir, 0)
	if len(snaps) != 1 || snaps[0].Kind != usage.KindResource {
		t.Fatalf("the read left %d snapshot(s) in the log, want one for the resource", len(snaps))
	}
	// The digest the door served, against the budget it serves under. The
	// slack is the header, the frame and the marker neutralisation; anything
	// beyond that is the budget no longer deciding the size.
	n := len(snaps[0].Digest)
	if n > search.ContextBudget+1024 {
		t.Errorf("the resource served %d bytes against a budget of %d", n, search.ContextBudget)
	}
	// And the fixture is really larger than the budget, so the number above is
	// the truncation's doing. If the indexer ever clipped these messages, this
	// test would stop biting rather than fail, and this is what says so.
	if n < search.ContextBudget/2 {
		t.Fatalf("the session came back in %d bytes, well under the %d budget: the fixture is not oversized any more",
			n, search.ContextBudget)
	}
	// And the line as written, which is what the log's room is measured in.
	longest := 0
	f, err := os.Open(usage.SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		if n := len(sc.Bytes()); n > longest {
			longest = n
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if longest == 0 {
		t.Fatal("the injection log is empty, so this measures nothing")
	}
	if longest > usage.RecordRoom {
		t.Errorf("a resource read wrote %d bytes into a log with %d of room per record: it will rotate on every write",
			longest, usage.RecordRoom)
	}
}
