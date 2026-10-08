package sources

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// OpenClaw 2026.9.9 stores an event of 1 KiB or more as a zstd frame in
// event_zstd with event_json null. Read for event_json alone, those events,
// a compaction summary among them, were not there at all.
func TestOpenClawReadsCompressedEvents(t *testing.T) {
	if !SQLite3Available() || !ZstdAvailable() {
		t.Skip("sqlite3 or zstd not installed")
	}
	const id = "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f"
	events := []string{
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-01T09:00:00Z","cwd":"/w/p"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-01T09:00:01Z","message":{"role":"user","content":"why does the pool stall"}}`,
		`{"type":"compaction","id":"c1","parentId":"u1","timestamp":"2026-10-01T09:00:02Z","summary":"ZSTD-SUMMARY ` + strings.Repeat("the pool stalled on the default timeout. ", 40) + `","firstKeptEntryId":"u1","tokensBefore":9000}`,
		`{"type":"message","id":"a1","parentId":"c1","timestamp":"2026-10-01T09:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"ZSTD-ANSWER ` + strings.Repeat("raise server_idle_timeout. ", 50) + `"}]}}`,
	}
	sql := "create table transcript_events (session_id text not null, seq integer not null, event_json text, created_at integer not null, event_zstd blob, event_utf8_bytes integer, navigation_json text, primary key (session_id, seq));\n"
	for i, e := range events {
		row := "'" + strings.ReplaceAll(e, "'", "''") + "',null,null"
		if len(e) >= 1024 {
			cmd := exec.Command("zstd", "-c", "-q", "-1")
			cmd.Stdin = strings.NewReader(e)
			frame, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			row = "null,X'" + hex.EncodeToString(frame) + "'," + strconv.Itoa(len(e))
		}
		sql += "insert into transcript_events (session_id,seq,event_json,event_zstd,event_utf8_bytes,created_at) values ('" + id + "'," + strconv.Itoa(i) + "," + row + ",1790000000000);\n"
	}
	db := filepath.Join(t.TempDir(), "main", "agent", "openclaw-agent.sqlite")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("sqlite3", db)
	build.Stdin = strings.NewReader(sql)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	check := func(t *testing.T) {
		t.Helper()
		ss, err := ParseOpenClawDB(db)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse: %v %d", err, len(ss))
		}
		var got []string
		for _, m := range ss[0].Messages {
			got = append(got, m.Role+":"+strings.Fields(m.Text)[0])
		}
		if want := "user:why|summary:ZSTD-SUMMARY|assistant:ZSTD-ANSWER"; strings.Join(got, "|") != want {
			t.Errorf("messages = %q, want %q", strings.Join(got, "|"), want)
		}
	}
	check(t)

	// A corrupt frame loses its own event, not the session's other ones.
	corrupt := exec.Command("sqlite3", db, "update transcript_events set event_zstd = X'28B52FFD00' where seq = 2")
	if out, err := corrupt.CombinedOutput(); err != nil {
		t.Fatalf("corrupt: %v: %s", err, out)
	}
	ss, err := ParseOpenClawDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	var roles []string
	for _, m := range ss[0].Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,assistant" || !bytes.Contains([]byte(ss[0].Messages[1].Text), []byte("ZSTD-ANSWER")) {
		t.Errorf("after a corrupt frame: %v", roles)
	}
}
