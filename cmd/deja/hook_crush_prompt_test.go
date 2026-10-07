package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Crush fires only PreToolUse, so its first tool call carries the session's
// digest, and the first one after a new message carries the recall for it,
// read out of crush.db. Each comes once.
func TestCrushToolHookCarriesTheDigestAndTheNewestMessagesRecall(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude")
	if err := os.MkdirAll(filepath.Join(claude, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cwd := filepath.Join(tmp, "proj")
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for i, text := range []string{
		"the retry_loop in fetcher drops the last attempt",
		"unrelated work about deployments and dashboards",
	} {
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%d","timestamp":%q,"cwd":%q,"message":{"role":"user","content":%q}}`,
			i, at, cwd, text)
		if err := os.WriteFile(filepath.Join(claude, "proj", fmt.Sprintf("s%d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	crushHome := filepath.Join(tmp, "crush-home")
	t.Setenv("DEJA_CRUSH_ROOT", crushHome)
	data := filepath.Join(cwd, ".crush")
	for _, d := range []string{crushHome, data} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	reg, _ := json.Marshal(map[string]any{"projects": []map[string]string{{"path": cwd, "data_dir": data}}})
	if err := os.WriteFile(filepath.Join(crushHome, "projects.json"), reg, 0o644); err != nil {
		t.Fatal(err)
	}
	text := func(s string) string {
		b, _ := json.Marshal([]map[string]any{{"type": "text", "data": map[string]any{"text": s}}})
		return "'" + strings.ReplaceAll(string(b), "'", "''") + "'"
	}
	sql := `create table sessions (id text primary key, parent_session_id text, title text not null,
  message_count integer not null default 0, prompt_tokens integer not null default 0,
  completion_tokens integer not null default 0, cost real not null default 0.0,
  updated_at integer not null, created_at integer not null, summary_message_id text, todos text);
create table messages (id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', model text, created_at integer not null, updated_at integer not null,
  finished_at integer, provider text, is_summary_message integer default 0 not null);
insert into sessions values ('c3',null,'t',1,0,0,0,100,100,null,null);
insert into messages values ('u1','c3','user',` + text("what did we change in the retry_loop in fetcher") + `,'m',100,100,null,'p',0);
`
	cmd := exec.Command("sqlite3", filepath.Join(data, "crush.db"))
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	tool, _ := json.Marshal(map[string]any{"event": "PreToolUse", "session_id": "c3", "cwd": cwd,
		"tool_name": "view", "tool_input": map[string]any{"file_path": filepath.Join(cwd, "fetcher.go")}})
	got := runDeferredHook(t, dir, "hook-tool", []string{"--crush"}, string(tool))
	var resp struct {
		Version int    `json:"version"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(got), &resp); err != nil || resp.Version != 1 {
		t.Fatalf("not Crush's shape: %q (%v)", got, err)
	}
	if !strings.Contains(resp.Context, "recall_context") {
		t.Errorf("the first tool call carried no digest: %q", resp.Context)
	}
	if !strings.Contains(resp.Context, "retry_loop") {
		t.Errorf("the first tool call did not answer the message: %q", resp.Context)
	}
	if again := runDeferredHook(t, dir, "hook-tool", []string{"--crush"}, string(tool)); strings.Contains(again, "retry_loop") || strings.Contains(again, "recall_context") {
		t.Fatalf("the digest or the recall came twice: %q", again)
	}
}
