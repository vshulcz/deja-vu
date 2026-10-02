package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	oldLoop = "\tfor {"
	newLoop = "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"
	jitter  = "func backoffJitter(attempt int) time.Duration { return 0 }"
)

// patch is one apply_patch body in the format codex, Copilot CLI, OpenClaw and
// Cline share, and patchWants what every reader must make of it.
var (
	patch      = "*** Begin Patch\n*** Update File: /tmp/proj/retry.go\n@@\n-" + oldLoop + "\n+" + newLoop + "\n*** Add File: /tmp/proj/jitter.go\n+" + jitter + "\n*** End Patch"
	patchWants = []string{
		vocabFiles("/tmp/proj/retry.go\n/tmp/proj/jitter.go"),
		vocabEdit("/tmp/proj/retry.go", oldLoop),
		vocabWrote("/tmp/proj/retry.go", newLoop),
		vocabWrote("/tmp/proj/jitter.go", jitter),
	}
)

// Each row is tool calls the way a harness's own client writes them, with the
// tool names and argument keys read off that client's registry, and the work
// records they must yield. A reader that knew the transport but not the
// vocabulary kept the calls as bare output: no command, no file, no edit and
// nothing for blame to attribute (#4489–#4506).
func TestToolVocabularyAcrossReaders(t *testing.T) {
	cases := []struct {
		name  string
		parse func(t *testing.T) []model.Session
		want  []string
	}{
		{"claude PowerShell and NotebookEdit", vocabClaude(ParseClaudeFile), []string{
			vocabCmd("$ go test ./..."),
			vocabFiles("/tmp/proj/retry.ipynb"),
			vocabWrote("/tmp/proj/retry.ipynb", "retries = compute_backoff_with_jitter(attempt, base=0.5)\n"),
		}},
		{"claude reference parser", vocabClaude(func(p string) ([]model.Session, error) { return parseClaudeGenericFromOffset(p, 0) }), []string{
			vocabCmd("$ go test ./..."),
			vocabFiles("/tmp/proj/retry.ipynb"),
			vocabWrote("/tmp/proj/retry.ipynb", "retries = compute_backoff_with_jitter(attempt, base=0.5)\n"),
		}},
		{"codex shell_command", vocabCodex, []string{
			vocabCmd("$ go test ./...  → exit 1"),
		}},
		{"copilot view and apply_patch", vocabCopilot(patch), append([]string{
			vocabFiles("/tmp/proj/view.go"),
		}, patchWants...)},
		{"copilot-chat readFile", vocabCopilotChat, []string{
			vocabFiles("/tmp/proj/retry.go"),
		}},
		{"gemini read_many_files", vocabGemini, []string{
			vocabFiles("/tmp/proj/many_a.go\n/tmp/proj/many_b.go"),
		}},
		{"opencode 1.x edit and write", vocabOpencodeV1, []string{
			vocabEdit("/tmp/proj/retry.go", oldLoop),
			vocabWrote("/tmp/proj/retry.go", newLoop),
			vocabWrote("/tmp/proj/backoff.go", jitter),
		}},
		{"opencode 2.x write", vocabOpencodeV2, []string{
			vocabWrote("/tmp/proj/backoff.go", jitter),
		}},
		{"grok search_replace and write", vocabGrok, []string{
			vocabFiles("/tmp/proj/retry.go"),
			vocabEdit("/tmp/proj/retry.go", oldLoop),
			vocabWrote("/tmp/proj/retry.go", newLoop),
			vocabFiles("/tmp/proj/jitter.go"),
			vocabWrote("/tmp/proj/jitter.go", jitter),
		}},
		{"grok-dev grok.db", vocabGrokDB, []string{
			vocabCmd("$ go test ./..."),
			RoleToolOutput + ": ./retry.go:12:5: undefined: backoffJitter",
			vocabFiles("/tmp/proj/read.go"),
			vocabFiles("/tmp/proj/retry.go"),
			vocabEdit("/tmp/proj/retry.go", oldLoop),
			vocabWrote("/tmp/proj/retry.go", newLoop),
			vocabWrote("/tmp/proj/jitter.go", jitter),
		}},
		{"openclaw apply_patch", vocabOpenClaw(patch), patchWants},
		{"cline-sdk editor and apply_patch", vocabClineSDK(patch), append([]string{
			vocabFiles("/tmp/proj/jitter.go"),
			vocabWrote("/tmp/proj/jitter.go", jitter),
			vocabEdit("/tmp/proj/retry.go", oldLoop),
		}, patchWants...)},
		{"cline-vscode replace_in_file and apply_patch", vocabClineVSCode(patch), append([]string{
			vocabEdit("/tmp/proj/backoff.go", "for retries := 0; ; retries++ {"),
			vocabWrote("/tmp/proj/backoff.go", "for attempt := 0; attempt < maxAttempts; attempt++ {"),
		}, patchWants...)},
		{"kiro-ide tool calls", vocabKiroIDE, []string{
			vocabCmd("$ go test ./retry"),
			vocabFiles("/tmp/proj/retry.go"),
			vocabEdit("/tmp/proj/retry.go", oldLoop),
			vocabWrote("/tmp/proj/retry.go", newLoop),
			vocabFiles("/tmp/proj/jitter.go"),
			vocabWrote("/tmp/proj/jitter.go", jitter),
			vocabWrote("/tmp/proj/notes.md", "retries are capped at maxAttempts with a jittered backoff"),
			vocabFiles("/tmp/proj/read.go"),
			vocabFiles("/tmp/proj/old.go"),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ss := c.parse(t)
			got := map[string]bool{}
			var all []string
			for _, s := range ss {
				for _, m := range s.Messages {
					got[m.Role+": "+m.Text] = true
					all = append(all, fmt.Sprintf("%s: %q", m.Role, m.Text))
				}
			}
			missing := false
			for _, w := range c.want {
				if !got[w] {
					missing = true
					t.Errorf("missing %q", w)
				}
			}
			if missing {
				t.Logf("got:\n  %s", strings.Join(all, "\n  "))
			}
		})
	}
}

func vocabCmd(s string) string   { return RoleCommand + ": " + s }
func vocabFiles(s string) string { return RoleFiles + ": " + s }
func vocabEdit(path, span string) string {
	return RoleEdit + ": " + path + "\n" + span
}
func vocabWrote(path, text string) string { return RoleWrote + ": " + WroteRecord(path, text) }

func vocabJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func vocabWrite(t *testing.T, path string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func vocabParse(t *testing.T, parse func(string) ([]model.Session, error), path string) []model.Session {
	t.Helper()
	ss, err := parse(path)
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

func vocabSQL(t *testing.T, script string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "store.db")
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

// Claude Code 2.1.287: PowerShell {command}, NotebookEdit {notebook_path,
// cell_id, new_source, edit_mode} (#4489).
func vocabClaude(parse func(string) ([]model.Session, error)) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		root := t.TempDir()
		t.Setenv("DEJA_CLAUDE_ROOT", root)
		line := func(content any) string {
			return vocabJSON(map[string]any{"type": "assistant", "sessionId": "c1", "timestamp": "2026-10-01T10:00:00Z", "cwd": "/tmp/proj",
				"message": map[string]any{"role": "assistant", "content": content}})
		}
		p := vocabWrite(t, filepath.Join(root, "-tmp-proj", "c1.jsonl"),
			`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"fix the retry loop"}}`,
			line([]any{map[string]any{"type": "tool_use", "id": "p1", "name": "PowerShell", "input": map[string]any{"command": "go test ./..."}}}),
			line([]any{map[string]any{"type": "tool_use", "id": "n1", "name": "NotebookEdit", "input": map[string]any{
				"notebook_path": "/tmp/proj/retry.ipynb", "cell_id": "c1", "new_source": "retries = compute_backoff_with_jitter(attempt, base=0.5)\n", "edit_mode": "replace"}}}),
		)
		return vocabParse(t, parse, p)
	}
}

// Codex 0.149: shell_command {command, workdir}, the shell whenever unified
// exec is off (#4490).
func vocabCodex(t *testing.T) []model.Session {
	rec := func(typ string, payload any) string {
		return vocabJSON(map[string]any{"timestamp": "2026-10-01T10:00:01.000Z", "type": typ, "payload": payload})
	}
	p := vocabWrite(t, filepath.Join(t.TempDir(), "rollout-2026-10-01T10-00-00-019a0000-1111-7222-8333-444455556666.jsonl"),
		rec("session_meta", map[string]any{"id": "019a0000-1111-7222-8333-444455556666", "cwd": "/tmp/proj"}),
		rec("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "fix the retry loop"}}}),
		rec("response_item", map[string]any{"type": "function_call", "name": "shell_command", "call_id": "c1",
			"arguments": vocabJSON(map[string]any{"command": "go test ./...", "workdir": "/tmp/proj"})}),
		rec("response_item", map[string]any{"type": "function_call_output", "call_id": "c1",
			"output": "Exit code: 1\nWall time: 0.4 seconds\nOutput:\n./retry.go:12:5: undefined: backoffJitter\nFAIL"}),
	)
	return vocabParse(t, ParseCodexRollout, p)
}

// Copilot CLI 1.0.79: view {path}, and with a GPT model apply_patch, whose
// arguments are the patch string itself (#4491).
func vocabCopilot(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		ev := func(name string, args any) string {
			return vocabJSON(map[string]any{"type": "tool.execution_start", "timestamp": "2026-10-01T10:00:01.000Z",
				"data": map[string]any{"toolCallId": name, "toolName": name, "arguments": args}})
		}
		p := vocabWrite(t, filepath.Join(t.TempDir(), "c0c0c0c0", "events.jsonl"),
			vocabJSON(map[string]any{"type": "session.start", "data": map[string]any{"sessionId": "c0c0c0c0", "startTime": "2026-10-01T10:00:00.000Z", "context": map[string]any{"cwd": "/tmp/proj"}}}),
			`{"type":"user.message","timestamp":"2026-10-01T10:00:00.500Z","data":{"content":"fix the retry loop"}}`,
			ev("view", map[string]any{"path": "/tmp/proj/view.go"}),
			ev("apply_patch", patch),
		)
		return vocabParse(t, ParseCopilotFile, p)
	}
}

// Copilot Chat 0.64.1: copilot_readFile keeps its target only in the
// message uris (#4492).
func vocabCopilotChat(t *testing.T) []model.Session {
	uris := map[string]any{"file:///tmp/proj/retry.go": map[string]any{"$mid": 1, "path": "/tmp/proj/retry.go", "scheme": "file"}}
	p := vocabWrite(t, filepath.Join(t.TempDir(), "chatSessions", "5c0ffee0-0000-4000-8000-000000000000.jsonl"),
		vocabJSON(map[string]any{"kind": 0, "v": map[string]any{"version": 3, "sessionId": "5c0ffee0-0000-4000-8000-000000000000", "creationDate": 1790000000000, "requests": []any{}}}),
		vocabJSON(map[string]any{"kind": 2, "k": []any{"requests"}, "v": []any{map[string]any{
			"requestId": "r1", "timestamp": 1790000001000, "message": map[string]any{"text": "fix the retry loop"},
			"response": []any{
				map[string]any{"kind": "toolInvocationSerialized", "toolId": "copilot_readFile", "toolCallId": "c1", "isComplete": true,
					"invocationMessage": map[string]any{"value": "Reading [](file:///tmp/proj/retry.go)", "uris": uris},
					"pastTenseMessage":  map[string]any{"value": "Read [](file:///tmp/proj/retry.go)", "uris": uris}},
				map[string]any{"value": "The retry loop never stops."},
			}}}}),
	)
	return vocabParse(t, ParseCopilotChatFile, p)
}

// Gemini CLI 0.60.0: read_many_files {include: [paths or globs]} (#4494).
func vocabGemini(t *testing.T) []model.Session {
	_, chats := geminiTree(t)
	call := map[string]any{"id": "rm1", "name": "read_many_files", "status": "success",
		"args": map[string]any{"include": []any{"/tmp/proj/many_a.go", "/tmp/proj/many_b.go", "src/**/*.go"}}}
	p := vocabWrite(t, filepath.Join(chats, "session-2026-10-02T10-00-sess-v-1.jsonl"),
		`{"sessionId":"sess-v-1","projectHash":"abc","startTime":"2026-10-02T10:00:00.000Z","lastUpdated":"2026-10-02T10:01:00.000Z","kind":"main"}`,
		`{"id":"u1","timestamp":"2026-10-02T10:00:01.000Z","type":"user","content":[{"text":"fix the retry loop"}]}`,
		`{"id":"g1","timestamp":"2026-10-02T10:00:02.000Z","type":"gemini","content":"","model":"luna","toolCalls":[`+vocabJSON(call)+`]}`,
	)
	return vocabParse(t, ParseGeminiFile, p)
}

// opencode 1.18: edit {filePath, oldString, newString}, write {filePath,
// content} (#4495).
func vocabOpencodeV1(t *testing.T) []model.Session {
	part := func(id, tool string, input any) string {
		// Spelled out rather than marshalled: opencode writes type and tool
		// first, and the reader's cheap gate looks for them there.
		data := fmt.Sprintf(`{"type":"tool","tool":%q,"callID":%q,"state":{"status":"completed","input":%s,"output":"ok","time":{"start":1790000002000}}}`, tool, id, vocabJSON(input))
		return fmt.Sprintf("insert into part values('%s','m1',%s);\n", id, sqlQuote(data))
	}
	db := vocabSQL(t, `create table session(id text primary key, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_1',null,'/tmp/proj','fix the retry loop',1790000000000,1790000100000);
insert into message values('m0','ses_1',1790000000000,'{"role":"user","time":{"created":1790000000000}}');
insert into part values('p0','m0','{"type":"text","text":"fix the retry loop"}');
insert into message values('m1','ses_1',1790000001000,'{"role":"assistant","time":{"created":1790000001000}}');
`+part("p1", "edit", map[string]any{"filePath": "/tmp/proj/retry.go", "oldString": "\tfor {", "newString": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"})+
		part("p2", "write", map[string]any{"filePath": "/tmp/proj/backoff.go", "content": "func backoffJitter(attempt int) time.Duration { return 0 }"}))
	return vocabParse(t, ParseOpencodeDB, db)
}

// opencode 2.0.22: write {path, content} (#4495).
func vocabOpencodeV2(t *testing.T) []model.Session {
	content := []any{map[string]any{"type": "tool", "id": "c1", "name": "write", "time": map[string]any{"start": 1790000002000},
		"state": map[string]any{"status": "completed", "input": map[string]any{"path": "/tmp/proj/backoff.go", "content": "func backoffJitter(attempt int) time.Duration { return 0 }"}}}}
	db := vocabSQL(t, `create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session_v2 values('s1','p1',null,'/tmp/proj','fix the retry loop',1790000000000,1790000100000);
insert into session_message values('m1','s1','user',1,1790000000000,1790000000000,'{"time":{"created":1790000000000},"text":"fix the retry loop"}');
insert into session_message values('m2','s1','assistant',2,1790000001000,1790000001000,`+sqlQuote(vocabJSON(map[string]any{"time": map[string]any{"created": 1790000001000}, "content": content}))+`);`)
	return vocabParse(t, ParseOpencodeDB, db)
}

// Grok Build 1.0.41: search_replace {file_path, old_string, new_string},
// write {file_path, content} (#4497).
func vocabGrok(t *testing.T) []model.Session {
	call := func(id, name string, in any) string {
		return vocabJSON(map[string]any{"timestamp": 1790874400002, "params": map[string]any{"update": map[string]any{
			"sessionUpdate": "tool_call", "toolCallId": id, "title": name, "rawInput": in,
			"_meta": map[string]any{"x.ai/tool": map[string]any{"name": name}}}}})
	}
	dir := filepath.Join(t.TempDir(), "sessions", "%2Ftmp%2Fproj", "01a0f900-0000-7000-8000-000000000001")
	vocabWrite(t, filepath.Join(dir, "summary.json"), `{"info":{"id":"01a0f900-0000-7000-8000-000000000001","cwd":"/tmp/proj"}}`)
	p := vocabWrite(t, filepath.Join(dir, "updates.jsonl"),
		`{"timestamp":1790874400001,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"fix the retry loop"}}}}`,
		call("c2", "search_replace", map[string]any{"file_path": "/tmp/proj/retry.go", "old_string": "\tfor {", "new_string": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}),
		call("c3", "write", map[string]any{"file_path": "/tmp/proj/jitter.go", "content": "func backoffJitter(attempt int) time.Duration { return 0 }"}),
	)
	return vocabParse(t, ParseGrokFile, p)
}

// grok-dev 1.1.7: AI SDK tool-call and tool-result parts in grok.db, bash
// {command}, read_file/write_file/edit_file {path, …} (#4498).
func vocabGrokDB(t *testing.T) []model.Session {
	var b strings.Builder
	b.WriteString(`CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('gd1','w1','fix the retry loop','/tmp/proj','2026-09-20T10:00:00.000Z');
`)
	seq := 0
	add := func(role string, msg any) {
		fmt.Fprintf(&b, "INSERT INTO messages VALUES ('gd1',%d,'%s',%s,'2026-09-20T10:00:%02d.000Z');\n", seq, role, sqlQuote(vocabJSON(msg)), seq)
		seq++
	}
	add("user", map[string]any{"role": "user", "content": "fix the retry loop"})
	for i, c := range []struct {
		name    string
		in, out map[string]any
	}{
		{"bash", map[string]any{"command": "go test ./..."}, map[string]any{"success": false, "error": "./retry.go:12:5: undefined: backoffJitter"}},
		{"read_file", map[string]any{"path": "/tmp/proj/read.go"}, map[string]any{"success": true, "output": "package proj"}},
		{"edit_file", map[string]any{"path": "/tmp/proj/retry.go", "old_string": "\tfor {", "new_string": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}, map[string]any{"success": true, "output": "Edited /tmp/proj/retry.go"}},
		{"write_file", map[string]any{"path": "/tmp/proj/jitter.go", "content": "func backoffJitter(attempt int) time.Duration { return 0 }"}, map[string]any{"success": true, "output": "Wrote /tmp/proj/jitter.go"}},
	} {
		id := fmt.Sprintf("c%d", i)
		add("assistant", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool-call", "toolCallId": id, "toolName": c.name, "input": c.in}}})
		add("tool", map[string]any{"role": "tool", "content": []any{map[string]any{"type": "tool-result", "toolCallId": id, "toolName": c.name, "output": map[string]any{"type": "json", "value": c.out}}}})
	}
	return vocabParse(t, func(db string) ([]model.Session, error) { return ParseGrokDBSince(db, time.Time{}) }, vocabSQL(t, b.String()))
}

// OpenClaw 2026.7.1: apply_patch {input}, on for every model (#4500).
func vocabOpenClaw(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		p := vocabWrite(t, filepath.Join(t.TempDir(), "agents", "main", "sessions", "oc-s1.jsonl"),
			`{"type":"session","version":3,"id":"oc-s1","timestamp":"2026-09-20T10:00:00.000Z","cwd":"/tmp/proj"}`,
			`{"type":"message","id":"u1","timestamp":"2026-09-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`,
			vocabJSON(map[string]any{"type": "message", "id": "a1", "timestamp": "2026-09-20T10:00:01.000Z", "message": map[string]any{"role": "assistant",
				"content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "apply_patch", "arguments": map[string]any{"input": patch}}}}}),
			`{"type":"message","id":"r1","timestamp":"2026-09-20T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"apply_patch","content":[{"type":"text","text":"Success. Updated the following files:\nM /tmp/proj/retry.go"}],"details":{},"isError":false}}`,
		)
		return vocabParse(t, ParseOpenClawFile, p)
	}
}

// Cline CLI 3.0.67 (@cline/core 0.0.89): editor {path, old_text?, new_text},
// apply_patch {input} (#4503).
func vocabClineSDK(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		use := func(id, name string, in any) any {
			return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}}}
		}
		doc := map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
			use("c1", "editor", map[string]any{"path": "/tmp/proj/jitter.go", "new_text": "func backoffJitter(attempt int) time.Duration { return 0 }"}),
			use("c2", "editor", map[string]any{"path": "/tmp/proj/retry.go", "old_text": "\tfor {", "new_text": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}),
			use("c3", "apply_patch", map[string]any{"input": patch}),
		}}
		p := vocabWrite(t, filepath.Join(t.TempDir(), "1790877871094_abcde", "1790877871094_abcde.messages.json"), vocabJSON(doc))
		return vocabParse(t, ParseClineFile, p)
	}
}

// Cline extension 3.81: replace_in_file {path, diff} with Cline's own
// markers, apply_patch {input} (#4504).
func vocabClineVSCode(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		use := func(id, name string, in any) any {
			return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}}}
		}
		doc := []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
			use("t1", "replace_in_file", map[string]any{"path": "/tmp/proj/backoff.go",
				"diff": "------- SEARCH\nfor retries := 0; ; retries++ {\n=======\nfor attempt := 0; attempt < maxAttempts; attempt++ {\n+++++++ REPLACE"}),
			use("t2", "apply_patch", map[string]any{"input": patch}),
		}
		p := vocabWrite(t, filepath.Join(t.TempDir(), "tasks", "1790000000000", "api_conversation_history.json"), vocabJSON(doc))
		return vocabParse(t, ParseClineFile, p)
	}
}

// kiro-cli --v3 and the Kiro IDE (@kiro/agent 0.66.0): tool_call records with
// toolName and args (#4506).
func vocabKiroIDE(t *testing.T) []model.Session {
	dir := filepath.Join(t.TempDir(), "f045c81011ce49e2", "sess_00000000-0000-4000-8000-0000000000aa")
	n := 0
	rec := func(payload any) string {
		n++
		return vocabJSON(map[string]any{"id": fmt.Sprintf("r%d", n), "timestamp": fmt.Sprintf("2026-10-01T10:00:%02d.000Z", n), "payload": payload})
	}
	call := func(id, name string, args any) string {
		return rec(map[string]any{"type": "tool_call", "toolCallId": id, "toolName": name, "args": args, "status": "completed"})
	}
	p := vocabWrite(t, filepath.Join(dir, "messages.jsonl"),
		rec(map[string]any{"type": "user", "content": "fix the retry loop"}),
		call("c1", "execute_bash", map[string]any{"command": "go test ./retry", "cwd": "/tmp/proj"}),
		rec(map[string]any{"type": "tool_result", "toolCallId": "c1", "content": "Output: ./retry.go:12:5: undefined: backoffJitter  Exit Code: 1", "success": false}),
		call("c2", "str_replace", map[string]any{"path": "/tmp/proj/retry.go", "oldStr": "\tfor {", "newStr": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}),
		call("c3", "fs_write", map[string]any{"path": "/tmp/proj/jitter.go", "text": "func backoffJitter(attempt int) time.Duration { return 0 }"}),
		call("c4", "fs_append", map[string]any{"path": "/tmp/proj/notes.md", "text": "retries are capped at maxAttempts with a jittered backoff"}),
		call("c5", "read_file", map[string]any{"path": "/tmp/proj/read.go"}),
		call("c6", "delete_file", map[string]any{"targetFile": "/tmp/proj/old.go"}),
	)
	return vocabParse(t, ParseKiroIDEFile, p)
}
