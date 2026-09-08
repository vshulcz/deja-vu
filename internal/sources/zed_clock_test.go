package sources

import (
	"fmt"
	"testing"
)

// A thread that reads the same file twice and runs the same command twice —
// ordinary work, and what a real Zed thread does all day. Every message
// carried the thread's start, so the ingest's duplicate check saw one turn
// where there were two: 1385 of 6767 messages from the 30 real threads on
// this machine never reached the index (#3333).
const zedRepeatBody = `{"version":"0.3.0","title":"repeated work","updated_at":"2026-07-19T09:00:02Z","messages":[
{"User":{"id":"u1","content":[{"Text":"the retry queue stalls on staging"}]}},
{"Agent":{"content":[
 {"ToolUse":{"id":"c1","name":"read_file","input":{"path":"/w/app/queue/retry.go","start_line":1,"end_line":80}}},
 {"ToolUse":{"id":"c2","name":"terminal","input":{"command":"go test ./queue/...","cd":"/w/app"}}}
],"tool_results":{},"reasoning_details":null}},
{"Agent":{"content":[
 {"ToolUse":{"id":"c3","name":"read_file","input":{"path":"/w/app/queue/retry.go","start_line":1,"end_line":80}}},
 {"ToolUse":{"id":"c4","name":"terminal","input":{"command":"go test ./queue/...","cd":"/w/app"}}}
],"tool_results":{},"reasoning_details":null}}
]}`

func TestZedTellsTwoIdenticalTurnsApartByTheirPlace(t *testing.T) {
	zedHome(t)
	hex := zedZstdHex(t, zedRepeatBody)
	sql := fmt.Sprintf("%s\ninsert into threads (id, summary, updated_at, data_type, data, folder_paths, created_at) values ('r1', 'repeated work', '2026-07-19T09:00:02Z', 'zstd', x'%s', '[\"/w/app\"]', '2026-07-19T08:00:00Z');", zedSchema, hex)
	db := zedTestDB(t, sql)
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	ss, err := ParseZedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	seen := map[string]int{}
	for _, m := range ss[0].Messages {
		seen[m.Role+"\x00"+m.Text+"\x00"+m.Time.Format("2006-01-02T15:04:05.000000000Z07:00")]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("%d messages share role, text and stamp — the ingest stores one of them: %q", n, k)
		}
	}
	// The stamps stay inside the thread's own span and keep the array's order.
	prev := ss[0].Messages[0].Time
	for _, m := range ss[0].Messages[1:] {
		if m.Time.Before(prev) {
			t.Errorf("message stamped before the one ahead of it: %v < %v", m.Time, prev)
		}
		prev = m.Time
	}
	if last := ss[0].Messages[len(ss[0].Messages)-1].Time; last.After(ss[0].Updated) {
		t.Errorf("last message stamped %v, after the thread's own update %v", last, ss[0].Updated)
	}
}
