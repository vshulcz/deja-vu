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
	encoded := "Users-x-work-my-app"
	wantProject := filepath.Join("my", "app") // fallback splits on every hyphen
	if runtime.GOOS != "windows" {
		real := filepath.Join(tmp, "work", "my-app")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		encoded = strings.TrimPrefix(strings.ReplaceAll(real, string(filepath.Separator), "-"), "-")
		wantProject = "my-app" // resolved against the real directory
	}
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
	if ss[0].Project != wantProject {
		t.Fatalf("project = %q, want %q", ss[0].Project, wantProject)
	}
}

func TestCursorTranscriptProjectWalkUp(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "project directory",
			path: filepath.Join("root", "projects", "Users-me-work-app", "agent-transcripts", "session", "session.jsonl"),
			want: filepath.Join("work", "app"),
		},
		{
			name: "project directory case",
			path: filepath.Join("root", "Projects", "Users-me-work-app", "agent-transcripts", "session", "session.jsonl"),
			want: filepath.Join("work", "app"),
		},
		{
			name: "filesystem root",
			path: filepath.Join(string(filepath.Separator), "session.jsonl"),
			want: "-",
		},
		{
			name: "relative path without projects directory",
			path: filepath.Join("agent-transcripts", "session.jsonl"),
			want: "-",
		},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name string
			path string
			want string
		}{
			name: "windows drive root",
			path: filepath.Join(`C:\`, "session.jsonl"),
			want: "-",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cursorTranscriptProject(tt.path); got != tt.want {
				t.Fatalf("cursorTranscriptProject(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
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

func TestCursorTranscriptsPathMatchingIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)
	dir := filepath.Join(root, "projects", "Users-x-app", "Agent-Transcripts", "s1", "SubAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "child.JSONL")
	if err := os.WriteFile(path, []byte(`{"role":"user","message":{"content":"hi"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CursorTranscripts(); len(got) != 0 {
		t.Fatalf("subagent transcript discovered: %v", got)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if got := CursorTranscripts(); len(got) != 1 || got[0] != path {
		t.Fatalf("case-insensitive transcript discovery = %v, want %q", got, path)
	}
}

// The vocabulary here is not invented: it was read off a transcript the Cursor
// CLI wrote while being asked to read, edit, create, delete and run something.
// Every field differs from Claude's — `path` not `file_path`, `Shell` not
// `Bash` — which is why the dialect exists.
func TestCursorTranscriptWorkRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "Users-me-app", "agent-transcripts", "s-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"user","message":{"content":[{"type":"text","text":"fix the greeting"}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/w/app/main.go"}}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"StrReplace","input":{"path":"/w/app/main.go","old_string":"println(\"hello\")","new_string":"println(\"goodbye\")"}}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"path":"/w/app/notes.txt","contents":"alpha\n"}},{"type":"tool_use","name":"Delete","input":{"path":"/w/app/notes.txt"}}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"go test ./...","description":"tests"}},{"type":"tool_use","name":"Shell","input":{"command":"ls -la"}}]}}
{"type":"turn_ended","status":"success"}
`
	p := filepath.Join(dir, "s-work.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCursorTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	// A turn that only called tools carries no prose; its records must survive
	// anyway, which is the whole point of the work-record path.
	files := strings.Join(byRole[RoleFiles], "\n")
	for _, want := range []string{"/w/app/main.go", "/w/app/notes.txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("files records missing %q: %q", want, files)
		}
	}
	if len(byRole[RoleEdit]) != 1 || !strings.HasPrefix(byRole[RoleEdit][0], "/w/app/main.go\n") ||
		!strings.Contains(byRole[RoleEdit][0], `println("hello")`) {
		t.Errorf("edit span = %q, want the replaced bytes under their path", byRole[RoleEdit])
	}
	// `ls -la` is navigation and is dropped by the same rule Claude's commands
	// go through.
	if len(byRole[RoleCommand]) != 1 || byRole[RoleCommand][0] != "$ go test ./..." {
		t.Errorf("commands = %q, want only the meaningful one", byRole[RoleCommand])
	}
}

func TestCursorWorkRecordsRespectTheirSwitches(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "Users-me-app", "agent-transcripts", "s-off")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/w/app/main.go"}},{"type":"tool_use","name":"StrReplace","input":{"path":"/w/app/main.go","old_string":"a","new_string":"b"}},{"type":"tool_use","name":"Shell","input":{"command":"go build ./..."}}]}}
{"role":"user","message":{"content":[{"type":"text","text":"keep at least one message"}]}}
`
	p := filepath.Join(dir, "s-off.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	ss, err := ParseCursorTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles || m.Role == RoleEdit || m.Role == RoleCommand {
			t.Fatalf("a switched-off record was still written: %s %q", m.Role, m.Text)
		}
	}
}
