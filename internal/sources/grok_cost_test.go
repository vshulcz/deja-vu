package sources

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bigGrokUpdates writes a session shaped like the one in #1321: a few thousand
// ACP lines, most of them tool events carrying large payloads.
func bigGrokUpdates(t *testing.T, lines int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions", "proj", "01a00feb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"01a00feb","cwd":"/work/app"},"generated_title":"Fix the parser","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("build output line with paths and errors ", 100)
	var b strings.Builder
	for i := 0; i < lines; i++ {
		switch i % 20 {
		case 0:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"question %d about the build"},"_meta":{"promptIndex":%d}}}}`+"\n", 1782900000+i, i, i)
		case 1:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer %d"}},"_meta":{"promptId":"p%d"}}}`+"\n", 1782900000+i, i, i)
		default:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"call_%d","title":"bash","kind":"execute","status":"completed","content":[{"type":"content","content":{"type":"text","text":%q}}]}}}`+"\n", 1782900000+i, i, payload)
		}
	}
	p := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func parseGrokTiming(t *testing.T, lines int) (time.Duration, map[string]int) {
	t.Helper()
	p := bigGrokUpdates(t, lines)
	started := time.Now()
	ss, err := ParseGrokFile(p)
	took := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{}
	for _, s := range ss {
		for _, m := range s.Messages {
			roles[m.Role]++
		}
	}
	return took, roles
}

// The session shape from #1321 is mostly tool events, and the parser used to
// skip those lines before decoding them — which is why it was cheap and why it
// found nothing. Indexing the run costs reading the run.
//
// The bar is linearity rather than a stopwatch reading: this suite runs under
// `-race` on CI, where the same parse takes 6 s instead of 0.17 s, so an
// absolute bound measures the machine. Four times the lines should cost about
// four times as much; sixteen would mean something has gone quadratic.
func TestGrokIndexesTheRunWithoutGoingQuadratic(t *testing.T) {
	// The best of three pairs, not one. A ratio of two short timings on a
	// shared runner measures the runner: the same parse is a steady 4.0x here
	// and came back 8.6x once on the windows leg, where the 500-line baseline
	// read 62ms against 370ms on this machine — the small number is the one
	// noise distorts, and it distorts the ratio upward. Taking the smallest
	// ratio cannot hide a parse that has gone quadratic, because every pair
	// would read about 16x.
	best := math.Inf(1)
	var roles map[string]int
	for attempt := 0; attempt < 3; attempt++ {
		small, _ := parseGrokTiming(t, 500)
		large, r := parseGrokTiming(t, 2000)
		roles = r
		t.Logf("500 lines in %v, 2000 lines in %v; roles=%v", small, large, roles)
		if small <= 0 {
			continue
		}
		if ratio := float64(large) / float64(small); ratio < best {
			best = ratio
		}
	}
	if roles[RoleToolOutput] == 0 {
		t.Fatal("the run is not indexed: no tool records from a session that is mostly tool events")
	}
	if math.IsInf(best, 1) {
		t.Skip("timer resolution too coarse to compare")
	}
	if best > 8 {
		t.Errorf("4x the lines cost %.1fx the time at best of three, which is not linear", best)
	}
}
