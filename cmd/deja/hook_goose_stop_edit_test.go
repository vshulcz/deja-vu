package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// goose drops what PreToolUse prints, so the line deja gives before an edit
// rides the turn's Stop block, read from the editor call in sessions.db, once.
func TestGooseStopCarriesTheEditedFilesLine(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(tmp, "goose-store")
	t.Setenv("DEJA_GOOSE_ROOT", store)
	if err := os.MkdirAll(filepath.Join(store, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('g1','n','d','/work/alpha','2026-10-07T10:00:00Z','2026-10-07T10:05:00Z');
insert into messages values (1,'g1','user','[{"type":"text","text":"tidy the loader"}]',100);
insert into messages values (2,'g1','assistant','[{"type":"toolRequest","id":"c","toolCall":{"status":"success","value":{"name":"edit","arguments":{"path":"/work/alpha/config.go","before":"a","after":"b"}}}}]',101);
insert into messages values (3,'g1','user','[{"type":"toolResponse","id":"c","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"ok"}]}}}]',102);`
	if out, err := exec.Command("sqlite3", filepath.Join(store, "sessions", "sessions.db"), sql).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	stop := `{"event":"Stop","session_id":"g1","working_dir":"/work/alpha"}`
	got := runDeferredHook(t, dir, "hook-stop", nil, stop)
	var resp struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(got), &resp); err != nil || resp.Decision != "block" || !strings.Contains(resp.Reason, "sentinel") {
		t.Fatalf("Stop did not block with the edited file's line: %q (%v)", got, err)
	}
	if again := runDeferredHook(t, dir, "hook-stop", nil, stop); strings.Contains(again, "sentinel") {
		t.Fatalf("the same line came twice: %q", again)
	}
}

// goose has no status line; a SessionStart hook's {"banner"} is printed to
// the person once when an interactive session opens.
func TestGooseSessionStartPrintsTheStatusBanner(t *testing.T) {
	tmp := hermeticEnv(t)
	var out bytes.Buffer
	gooseStatusBanner(filepath.Join(tmp, "index.db"), []byte(`{"event":"SessionStart","session_id":"g1","working_dir":"/work/alpha"}`), &out)
	var resp struct {
		Banner string `json:"banner"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || !strings.HasPrefix(resp.Banner, "deja") {
		t.Fatalf("no deja banner: %q (%v)", out.String(), err)
	}
	out.Reset()
	t.Setenv("DEJA_RECALL", search.RecallOff)
	gooseStatusBanner(filepath.Join(tmp, "index.db"), nil, &out)
	if out.Len() != 0 {
		t.Fatalf("a banner with recall off: %q", out.String())
	}
}
