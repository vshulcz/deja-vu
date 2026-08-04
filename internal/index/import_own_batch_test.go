package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A shared folder is an outbox as well as an inbox: the machine that wrote a
// batch imports that same directory, and its own records came back as a second
// "imported" copy of every session it holds (#987).
func TestImportSkipsThisMachinesOwnBatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	claude := filepath.Join(tmp, "claude", "-tmp-x31")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	rec := `{"type":"user","message":{"role":"user","content":"first session about the pool cap"},"timestamp":"2026-07-12T10:00:00Z","sessionId":"b31","cwd":"/tmp/x31"}` + "\n"
	if err := os.WriteFile(filepath.Join(claude, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportFull(dir, shared); err != nil {
		t.Fatal(err)
	}
	n, err := Import(dir, shared)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("this machine imported its own batch: %d records", n)
	}
	if ImportSkippedOwn() == 0 {
		t.Errorf("the own-copy drop was not reported")
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.Project, "imported:") {
			t.Errorf("a session of this machine came back as imported: %s · %s", m.Project, m.ID)
		}
	}

	// The backside: a peer sends genuinely new lines for a session id this
	// machine also holds, and those must still land.
	peer := filepath.Join(tmp, "from-peer")
	if err := os.MkdirAll(peer, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "b31", Project: "tmp/x31", Role: "user",
		Text: "a new line the peer added to the same session", Time: time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peer, "deja-sync-p.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(dir, peer); err != nil || n != 1 {
		t.Errorf("a peer's new line for a shared session id was dropped: %d records, %v", n, err)
	}
	if ImportSkippedOwn() != 0 {
		t.Errorf("a peer's line was counted as this machine's own: %d", ImportSkippedOwn())
	}
}
