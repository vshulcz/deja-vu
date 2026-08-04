package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A note has no id that survives the crossing: buckets are the reader's local
// day (#911), so the peer sends the same line under an id this machine has
// never seen and the tombstone missed it — deja announced back the line that
// was just deleted (#985).
func TestAForgottenNoteStaysGoneWhenAPeerSendsTheirOwnBucket(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	when := time.Date(2026, 8, 4, 10, 32, 12, 0, time.UTC)
	body := `{"ts":"` + when.Format(time.RFC3339Nano) + `","project":"x29","text":"the ticker window stays at 30s"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	local := ""
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "deja-20") {
			local = m.ID
		}
	}
	if local == "" {
		t.Fatal("the note did not land in a day bucket")
	}
	if _, err := Forget(dir, ForgetOptions{Session: "deja:" + local}); err != nil {
		t.Fatal(err)
	}

	// The peer's copy: same text, same instant, their rendering of the day.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{Harness: "deja", SessionID: "deja-2026-08-03-x29",
		Project: "x29", Role: "user", Text: "the ticker window stays at 30s", Time: when})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := Import(dir, exp)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the forgotten note came back under the peer's bucket: %d records", n)
	}
	if ImportSkippedForgotten() != 1 {
		t.Errorf("the drop was not reported as a forgotten one: %d", ImportSkippedForgotten())
	}

	// The list someone reads names the note once, not once per record.
	for _, k := range Tombstones() {
		if strings.Contains(k, "\x00") {
			t.Errorf("a content key reached the list people read: %q", k)
		}
	}
	if got := TombstoneMatches("deja:" + local); got != 1 {
		t.Errorf("one forgotten note counted as %d sessions", got)
	}

	// Bringing it back has to bring back what the peer still sends, or the
	// next import would drop it in silence.
	if lifted, err := Unforget(dir, "deja:"+local, nil); err != nil || lifted != 1 {
		t.Fatalf("unforget lifted %d: %v", lifted, err)
	}
	if n, err := Import(dir, exp); err != nil || n != 1 {
		t.Errorf("after unforget the peer's copy still could not land: %d records, %v", n, err)
	}
}
