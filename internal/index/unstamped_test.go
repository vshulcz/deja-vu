package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A record whose message carried no timestamp round-trips through records.bin
// as time.Time{}.UnixNano(), which decodes to the year 1754 — never IsZero().
// Export then compares it as "very old" against a watermark of 0 and skips it
// on the first push and every push after, so those messages never reach
// another machine and nothing reports the loss.
func TestZeroTimeRoundTripsAsZero(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(f, newRecordTables())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "user", Text: "unstamped message"}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	var got Record
	if err := eachRecord(p, newRecordTables(), func(r Record) { got = r }); err != nil {
		t.Fatal(err)
	}
	if !got.Time.IsZero() {
		t.Fatalf("unstamped record decoded as %v, want the zero time — export treats it as ancient and never sends it", got.Time)
	}
	// A real timestamp must still survive untouched.
	when := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	f2, err := os.OpenFile(filepath.Join(dir, "r2.bin"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	rw2, err := newRecordWriter(f2, newRecordTables())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw2.write(Record{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "user", Text: "stamped", Time: when}); err != nil {
		t.Fatal(err)
	}
	if err := rw2.Close(); err != nil {
		t.Fatal(err)
	}
	var got2 Record
	if err := eachRecord(filepath.Join(dir, "r2.bin"), newRecordTables(), func(r Record) { got2 = r }); err != nil {
		t.Fatal(err)
	}
	if !got2.Time.Equal(when) {
		t.Fatalf("stamped record decoded as %v, want %v", got2.Time, when)
	}
}

// End to end: an unstamped message must reach a peer.
func TestUnstampedMessagesReachThePeer(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// No "timestamp" field at all — the harness never stamped this message.
	line := `{"type":"user","sessionId":"s1","message":{"role":"user","content":"unstamped but real work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	n, _, err := ExportDeferred(dir, filepath.Join(tmp, "out"), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("unstamped message was never exported — it can never reach another machine")
	}
}
