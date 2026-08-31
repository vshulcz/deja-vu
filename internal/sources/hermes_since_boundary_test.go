package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The watermark is the newest message deja has, and the filter has to hand back
// everything after it — including the messages written in the same instant.
//
// The column is REAL seconds, and the filter compared against whole seconds
// with a strict `>`. On a store that writes fractional stamps that over-reads
// harmlessly; on one that writes whole seconds, every other message of the
// watermark's own second is skipped, permanently. Measured before the fix:
// whole seconds handed back 0 of 2, fractional handed back 2 of 2 (#2075).
func TestHermesHandsBackTheWatermarksOwnSecond(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	const at = int64(1767268800)
	for _, tc := range []struct{ name, a, b string }{
		{"whole seconds", "1767268800", "1767268800"},
		{"fractional", "1767268800.2", "1767268800.7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "state.db")
			stmts := `create table messages (id integer primary key, session_id text, role text, content text, timestamp real);` +
				`insert into messages values (1,'s1','user','first turn',` + tc.a + `);` +
				`insert into messages values (2,'s1','user','second turn',` + tc.b + `);`
			if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
				t.Fatalf("seed: %v %s", err, out)
			}
			ss, err := ParseHermesDBSince(db, time.Unix(at, 0))
			if err != nil {
				t.Fatal(err)
			}
			var text []string
			for _, s := range ss {
				for _, m := range s.Messages {
					text = append(text, m.Text)
				}
			}
			joined := strings.Join(text, " | ")
			if !strings.Contains(joined, "second turn") {
				t.Errorf("a message stamped in the watermark's own second was skipped: %q", joined)
			}
		})
	}
}
