package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Every reader that knows the directory a session ran in labels its project
// the way Claude Code's does: the last two segments of that directory. Codex
// and others took the basename, so one directory was two projects and
// `--project w/my-app` dropped half of it (#4457); Cline and others folded the
// path into a folder key and decoded it back, which filed my-app under my/app
// whenever my/app existed (#4458); a file:// URI went in undecoded (#4461).
func TestRecordedCwdLabelsTheProjectLikeClaude(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"w/my-app", "w/my/app", "w/with space"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	myApp := filepath.Join(root, "w", "my-app")
	spaced := filepath.Join(root, "w", "with space")
	spacedURI := "file://" + strings.ReplaceAll(filepath.ToSlash(spaced), " ", "%20")
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	write := func(t *testing.T, p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fixture := func(t *testing.T, rel, from, to string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", rel))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), from, to)
	}
	sqlite := func(t *testing.T, db, stmts string) string {
		t.Helper()
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sqlite3", db)
		cmd.Stdin = strings.NewReader(stmts)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v\n%s", err, out)
		}
		return db
	}
	user := `fix the retry loop`

	cases := []struct {
		name, kind, cwd, want string
		store                 func(t *testing.T, home, cwd string) string
	}{
		{"codex", "codex", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			body := fixture(t, "codex/rollout-2026-07-17T09-00-00-registry-codex.jsonl", "/workspace/registry-demo", strings.Trim(js(cwd), `"`))
			return write(t, filepath.Join(home, ".codex", "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-0199a000-1111-7222-8333-444455556666.jsonl"), body)
		}},
		{"goose", "goose-jsonl", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			body := fixture(t, "goose/sessions/20250724_1.jsonl", "/workspace/demo", strings.Trim(js(cwd), `"`))
			return write(t, filepath.Join(home, ".local", "share", "goose", "sessions", "20250724_1.jsonl"), body)
		}},
		{"goose db", "goose-db", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			stmts := fixture(t, "goose/sessions/goose.sql", "/workspace/demo", strings.ReplaceAll(cwd, "'", "''"))
			return sqlite(t, filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db"), stmts)
		}},
		{"opencode", "opencode", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			db := filepath.Join(home, "opencode.db")
			t.Setenv("DEJA_OPENCODE_DB", db)
			return sqlite(t, db, fixture(t, "opencode/opencode.sql", "/workspace/registry-demo", strings.ReplaceAll(cwd, "'", "''")))
		}},
		{"kimi", "kimi", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			sd := filepath.Join(home, ".kimi-code", "sessions", "wd_0123456789ab", "session_0001")
			write(t, filepath.Join(sd, "state.json"), `{"createdAt":"2026-07-01T10:00:00.000Z","title":"retry","workDir":`+js(cwd)+`}`)
			wire := fixture(t, "kimi/sessions/wd_demo_0123456789ab/session_fixture01/agents/main/wire.jsonl", "", "")
			return write(t, filepath.Join(sd, "agents", "main", "wire.jsonl"), wire)
		}},
		{"grok", "grok", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			sd := filepath.Join(home, ".grok", "sessions", "w", "00000001-1111-4222-8333-444455556666")
			write(t, filepath.Join(sd, "summary.json"), `{"info":{"id":"00000001-1111-4222-8333-444455556666","cwd":`+js(cwd)+`},"created_at":"2026-07-17T09:00:00Z"}`)
			return write(t, filepath.Join(sd, "updates.jsonl"), `{"timestamp":1784278801,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"`+user+`"},"_meta":{"promptIndex":0}}}}`+"\n")
		}},
		{"dsh", "deepseek", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			return write(t, filepath.Join(home, ".dsh", "sessions", "--w-my-app--", "session-s1", "session.jsonl"),
				`{"type":"session","version":0,"id":"session-s1","createdAt":1787320263519,"cwd":`+js(cwd)+`}`+"\n"+
					`{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"`+user+`"}],"source":{"kind":"user"},"role":"user"}}`+"\n")
		}},
		{"amp", "amp", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			return write(t, filepath.Join(home, ".local", "share", "amp", "threads", "T-1.json"),
				`{"id":"T-1","title":"retry","created":1767337445000,"env":{"initial":{"trees":[{"uri":`+js("file://"+filepath.ToSlash(cwd))+`}]}},"messages":[{"role":"user","content":[{"type":"text","text":"`+user+`"}]}]}`)
		}},
		{"aider", "aider", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			return write(t, filepath.Join(cwd, ".aider.chat.history.md"), fixture(t, "aider/.aider.chat.history.md", "", ""))
		}},
		{"cline", "cline-sdk", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			r := filepath.Join(home, "cline-sessions")
			t.Setenv("DEJA_CLINE_ROOT", r)
			write(t, filepath.Join(r, "session_1", "session_1.json"), `{"session_id":"session_1","created_at":"2026-01-01T00:00:00.000Z","cwd":`+js(cwd)+`,"workspace_root":`+js(cwd)+`,"prompt":"`+user+`"}`)
			return write(t, filepath.Join(r, "session_1", "session_1.messages.json"), `{"version":1,"agent":"lead","sessionId":"session_1","messages":[{"role":"user","content":[{"type":"text","text":"`+user+`"}],"ts":1767225600000}]}`)
		}},
		{"roo cli", "roo", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			r := filepath.Join(home, "roo")
			t.Setenv("DEJA_ROO_CLI_ROOT", r)
			dir := filepath.Join(r, "tasks", "00000001-1111-4222-8333-444455556666")
			write(t, filepath.Join(dir, "history_item.json"), `{"id":"00000001-1111-4222-8333-444455556666","number":1,"ts":1767225700000,"task":"`+user+`","workspace":`+js(cwd)+`}`)
			return write(t, filepath.Join(dir, "api_conversation_history.json"), `[{"role":"user","content":[{"type":"text","text":"<task>\n`+user+`\n</task>"}]}]`)
		}},
		{"continue", "continue", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			r := filepath.Join(home, ".continue")
			t.Setenv("DEJA_CONTINUE_ROOT", r)
			return write(t, filepath.Join(r, "sessions", "00000001.json"), `{"sessionId":"00000001","title":"retry","workspaceDirectory":`+js(cwd)+`,"history":[{"message":{"role":"user","content":"`+user+`"},"contextItems":[]}]}`)
		}},
		{"continue uri", "continue", spacedURI, "w/with space", func(t *testing.T, home, cwd string) string {
			r := filepath.Join(home, ".continue")
			t.Setenv("DEJA_CONTINUE_ROOT", r)
			return write(t, filepath.Join(r, "sessions", "00000002.json"), `{"sessionId":"00000002","title":"retry","workspaceDirectory":`+js(cwd)+`,"history":[{"message":{"role":"user","content":"`+user+`"},"contextItems":[]}]}`)
		}},
		{"codewhale", "codewhale", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			r := filepath.Join(home, ".codewhale", "sessions")
			t.Setenv("DEJA_CODEWHALE_ROOT", r)
			return write(t, filepath.Join(r, "00000001.json"), `{"schema_version":3,"metadata":{"id":"00000001","title":"retry","created_at":"2026-09-19T08:00:00Z","workspace":`+js(cwd)+`},"messages":[{"role":"user","content":[{"type":"text","text":"`+user+`"}]}]}`)
		}},
		{"kiro cli", "kiro-cli", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			r := filepath.Join(home, ".kiro", "sessions")
			t.Setenv("DEJA_KIRO_ROOT", r)
			write(t, filepath.Join(r, "cli", "00000001.json"), `{"session_id":"00000001","cwd":`+js(cwd)+`,"created_at":"2026-08-01T10:00:00Z"}`)
			return write(t, filepath.Join(r, "cli", "00000001.jsonl"), `{"version":"v1","kind":"Prompt","data":{"message_id":"p1","content":[{"kind":"text","data":"`+user+`"}],"meta":{"timestamp":1785600005.42}}}`+"\n")
		}},
		{"reasonix", "reasonix", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			r := filepath.Join(home, ".reasonix")
			t.Setenv("REASONIX_STATE_HOME", r)
			p := filepath.Join(r, "projects", pathToProjectKey(cwd), "sessions", "20260920-101000.000000000-deepseek-chat.jsonl")
			write(t, p+".meta", `{"id":"20260920-101000.000000000-deepseek-chat","created_at":"2026-09-20T10:00:00Z","scope":"project","workspace_root":`+js(cwd)+`}`)
			return write(t, p, `{"role":"user","content":"`+user+`","createdAt":1789898405000}`+"\n")
		}},
		{"hermes", "hermes", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			return sqlite(t, filepath.Join(home, ".hermes", "state.db"), `create table messages (id integer primary key autoincrement, session_id text not null, role text not null, content text, tool_call_id text, tool_calls text, tool_name text, timestamp real not null, token_count integer, finish_reason text);
create table sessions (id text primary key, cwd text);
insert into messages(session_id,role,content,timestamp) values('h1','user','`+user+`',1785015600.25);
insert into sessions values('h1','`+strings.ReplaceAll(cwd, "'", "''")+`');`)
		}},
		{"copilot-chat", "copilot-chat", spacedURI, "w/with space", func(t *testing.T, home, cwd string) string {
			ws := filepath.Join(home, "Library", "Application Support", "Code", "User", "workspaceStorage", "a1b2c3d4e5f6")
			write(t, filepath.Join(ws, "workspace.json"), `{"folder":`+js(cwd)+`}`)
			rel := "copilot-chat/workspaceStorage/a1b2c3d4e5f6/chatSessions/3b7e1f1b-0000-4000-8000-000000000000.jsonl"
			return write(t, filepath.Join(ws, "chatSessions", "3b7e1f1b-0000-4000-8000-000000000000.jsonl"), fixture(t, rel, "file:///tmp/registry-demo", cwd))
		}},
		{"copilot-chat unc", "copilot-chat", "file://server/share/proj", "share/proj", func(t *testing.T, home, cwd string) string {
			ws := filepath.Join(home, "Library", "Application Support", "Code", "User", "workspaceStorage", "a1b2c3d4e5f6")
			write(t, filepath.Join(ws, "workspace.json"), `{"folder":`+js(cwd)+`}`)
			rel := "copilot-chat/workspaceStorage/a1b2c3d4e5f6/chatSessions/3b7e1f1b-0000-4000-8000-000000000000.jsonl"
			return write(t, filepath.Join(ws, "chatSessions", "3b7e1f1b-0000-4000-8000-000000000000.jsonl"), fixture(t, rel, "file:///tmp/registry-demo", cwd))
		}},
		{"copilot windows", "copilot", `C:\Users\x\my-app`, "x/my-app", func(t *testing.T, home, cwd string) string {
			id := "7cf44517-55ca-435c-893a-3fde1973a44e"
			body := fixture(t, "copilot/"+id+"/events.jsonl", "/Users/x/coding/gateway", strings.Trim(js(cwd), `"`))
			return write(t, filepath.Join(home, ".copilot", "session-state", id, "events.jsonl"), body)
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := filepath.Join(root, fmt.Sprintf("home%02d", i))
			t.Setenv("HOME", home)
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			p := c.store(t, home, c.cwd)
			ss := parseKindForTest(t, c.kind, p)
			if len(ss) == 0 {
				t.Fatalf("%s: no session from %s", c.kind, p)
			}
			for _, s := range ss {
				if s.Project != c.want {
					t.Errorf("%s labels %q as %q, want %q", c.kind, c.cwd, s.Project, c.want)
				}
			}
		})
	}
}

func parseKindForTest(t *testing.T, kind, p string) []model.Session {
	t.Helper()
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if k.Name == kind {
				ss, err := k.Parse(p, 0)
				if err != nil {
					t.Fatalf("parse %s: %v", p, err)
				}
				return ss
			}
		}
	}
	t.Fatalf("no kind %s", kind)
	return nil
}

// A workspace on a UNC share reopens on that share: the host is part of the
// path, and without it `code /share/proj` names a folder that is not there
// (#4462).
func TestCopilotChatWorkspaceDirKeepsTheUNCHost(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "a1b2c3d4e5f6")
	for uri, want := range map[string]string{
		"file://server/share/proj":   filepath.FromSlash("//server/share/proj"),
		"file:///tmp/w/with%20space": filepath.FromSlash("/tmp/w/with space"),
	} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "workspace.json"), []byte(`{"folder":"`+uri+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := CopilotChatWorkspaceDir(filepath.Join(ws, "chatSessions", "x.json")); got != want {
			t.Errorf("%s: workspace dir %q, want %q", uri, got, want)
		}
	}
}

// A drive is not a parent on any of the ways a Claude Code project gets its
// name: the folder name decoded once the directory is gone says C:\proj is
// "proj", as the recorded cwd does, or one directory is filed under two names
// depending on whether its transcripts carry a cwd.
func TestADriveRootDecodesToTheNameItsCwdGives(t *testing.T) {
	for _, cwd := range []string{`C:\proj`, `C:\a\b`, `D:\app`} {
		enc := claudeEncodePath(cwd)
		if got, want := decodeProjectBase(enc), cwdProjectName(cwd); got != want {
			t.Errorf("%s: folder %s decodes to %q, the cwd names %q", cwd, enc, got, want)
		}
	}
}
