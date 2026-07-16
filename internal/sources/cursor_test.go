package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseCursorDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "state.vscdb")
	schema := `create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:comp-1', json('{"composerId":"comp-1","name":"Fix the pager","createdAt":1752600000000,"lastUpdatedAt":1752600100000,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1},{"bubbleId":"b2","type":2}]}')),
 ('bubbleId:comp-1:b1', json('{"type":1,"text":"cursorneedle question","timestamp":1752600001000,"workspaceProjectDir":"/Users/me/work/my-app"}')),
 ('bubbleId:comp-1:b2', json('{"type":2,"text":"cursorneedle answer","rawText":"raw","timestamp":1752600002000}')),
 ('composerData:comp-empty', json('{"composerId":"comp-empty","name":"draft"}')),
 ('composerData:comp-null', null),
 ('agentKv:blob:x', 'not-json-target');`
	cmd := exec.Command("sqlite3", db, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v: %s", err, out)
	}
	ss, err := ParseCursorDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1 (empty/null dropped): %#v", len(ss), ss)
	}
	s := ss[0]
	if s.Harness != "cursor" || s.ID != "comp-1" || s.Title != "Fix the pager" {
		t.Fatalf("bad meta: %#v", s)
	}
	if s.Project != "my-app" {
		t.Fatalf("project = %q, want my-app", s.Project)
	}
	if len(s.Messages) != 2 || s.Messages[0].Role != "user" || s.Messages[1].Role != "assistant" {
		t.Fatalf("messages wrong: %#v", s.Messages)
	}
	if s.Messages[0].Time.UnixMilli() != 1752600001000 {
		t.Fatalf("timestamp wrong: %v", s.Messages[0].Time)
	}
}

func TestParseCursorTranscript(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "work", "my-app")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimPrefix(strings.ReplaceAll(real, string(filepath.Separator), "-"), "-")
	dir := filepath.Join(tmp, "cursorcli", "projects", encoded, "agent-transcripts", "sess-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"user","message":{"content":[{"type":"text","text":"transcriptneedle question"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"Running ls."},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
{"type":"turn_ended","status":"success"}
`
	p := filepath.Join(dir, "sess-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCursorTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "sess-1" {
		t.Fatalf("bad session: %#v", ss)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (control line skipped): %#v", len(ss[0].Messages), ss[0].Messages)
	}
	if runtime.GOOS != "windows" && ss[0].Project != "my-app" {
		// path resolution decodes unix-style absolute paths; the fallback
		// name is fine on windows
		t.Fatalf("project = %q, want my-app (greedy decode)", ss[0].Project)
	}
}

func TestCursorTranscriptsSkipSubagents(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)
	base := filepath.Join(root, "projects", "Users-x-app", "agent-transcripts", "s1")
	if err := os.MkdirAll(filepath.Join(base, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"role":"user","message":{"content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(base, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "subagents", "child.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	files := CursorTranscripts()
	if len(files) != 1 || !strings.HasSuffix(files[0], "s1.jsonl") {
		t.Fatalf("discovery wrong: %v", files)
	}
}
