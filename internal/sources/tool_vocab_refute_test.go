package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// vocabHas reports whether any session carries the record.
func vocabHas(ss []model.Session, want string) bool {
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role+": "+m.Text == want {
				return true
			}
		}
	}
	return false
}

// vocabRoles lists every record of the given role, for a failure message.
func vocabRoles(ss []model.Session, role string) []string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == role {
				out = append(out, m.Text)
			}
		}
	}
	return out
}

// A NotebookEdit in delete mode still carries new_source, and Claude Code
// discards it: no line of it reaches the notebook, so it is no wrote record
// (#4489).
func TestClaudeNotebookDeleteWritesNothing(t *testing.T) {
	const gone = "retries = compute_backoff_with_jitter(attempt, base=0.5)\n"
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"claude":    ParseClaudeFile,
		"reference": func(p string) ([]model.Session, error) { return parseClaudeGenericFromOffset(p, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("DEJA_CLAUDE_ROOT", root)
			p := vocabWrite(t, filepath.Join(root, "-tmp-proj", "c1.jsonl"),
				`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"drop the old cell"}}`,
				vocabJSON(map[string]any{"type": "assistant", "sessionId": "c1", "timestamp": "2026-10-01T10:00:01Z", "cwd": "/tmp/proj",
					"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "n1", "name": "NotebookEdit",
						"input": map[string]any{"notebook_path": "/tmp/proj/retry.ipynb", "cell_id": "c1", "new_source": gone, "edit_mode": "delete"}}}}}),
			)
			ss := vocabParse(t, parse, p)
			if !vocabHas(ss, vocabFiles("/tmp/proj/retry.ipynb")) {
				t.Errorf("the deleted cell's notebook is not a files record: %q", vocabRoles(ss, RoleFiles))
			}
			if w := vocabRoles(ss, RoleWrote); len(w) > 0 {
				t.Errorf("a deleted cell left wrote records %q", w)
			}
		})
	}
}

// Only copilot_readFile names in its message the file it opened. getErrors
// over the workspace lists every file with a problem, getChangedFiles the
// repository root, createDirectory a directory and memory a file of the
// extension's own — none a file the session touched (#4492).
func TestCopilotChatMessageURIsOnlyForReadFile(t *testing.T) {
	uri := func(paths ...string) map[string]any {
		out := map[string]any{}
		for _, p := range paths {
			out["file://"+p] = map[string]any{"$mid": 1, "path": p, "scheme": "file"}
		}
		return out
	}
	part := func(id, tool, msg string, uris map[string]any) any {
		return map[string]any{"kind": "toolInvocationSerialized", "toolId": tool, "toolCallId": id, "isComplete": true,
			"pastTenseMessage": map[string]any{"value": msg, "uris": uris}}
	}
	p := vocabWrite(t, filepath.Join(t.TempDir(), "chatSessions", "5c0ffee0-0000-4000-8000-000000000001.jsonl"),
		vocabJSON(map[string]any{"kind": 0, "v": map[string]any{"version": 3, "sessionId": "5c0ffee0-0000-4000-8000-000000000001", "creationDate": 1790000000000, "requests": []any{}}}),
		vocabJSON(map[string]any{"kind": 2, "k": []any{"requests"}, "v": []any{map[string]any{
			"requestId": "r1", "timestamp": 1790000001000, "message": map[string]any{"text": "fix the retry loop"},
			"response": []any{
				part("c1", "copilot_getErrors", "Checked workspace, 3 problems found in [](file:///tmp/proj/a.go), [](file:///tmp/proj/b.go)", uri("/tmp/proj/a.go", "/tmp/proj/b.go")),
				part("c2", "copilot_getChangedFiles", "Read changed files in [](file:///tmp/proj)", uri("/tmp/proj")),
				part("c3", "copilot_createDirectory", "Created [](file:///tmp/proj/pkg)", uri("/tmp/proj/pkg")),
				part("c4", "copilot_memory", "Read memory [](file:///tmp/storage/memory.md)", uri("/tmp/storage/memory.md")),
				part("c5", "copilot_readFile", "Read [](file:///tmp/proj/retry.go)", uri("/tmp/proj/retry.go")),
				map[string]any{"value": "The retry loop never stops."},
			}}}}),
	)
	ss := vocabParse(t, ParseCopilotChatFile, p)
	if got := vocabRoles(ss, RoleFiles); len(got) != 1 || got[0] != "/tmp/proj/retry.go" {
		t.Errorf("files records = %q, want only the file readFile opened", got)
	}
}

// A patch names files relative to where it runs: Copilot CLI and Cline resolve
// `*** Update File: retry.go` against the session's directory, and the record
// has to say /tmp/proj/retry.go for blame and restore to find it (#4491,
// #4503).
const relPatch = "*** Begin Patch\n*** Update File: retry.go\n@@\n-" + oldLoop + "\n+" + newLoop + "\n*** End Patch"

var relPatchWants = []string{
	vocabFiles("/tmp/proj/retry.go"),
	vocabEdit("/tmp/proj/retry.go", oldLoop),
	vocabWrote("/tmp/proj/retry.go", newLoop),
}

func TestRelativePatchPathsResolveAgainstTheSession(t *testing.T) {
	for name, parse := range map[string]func(t *testing.T) []model.Session{
		"copilot": vocabCopilot(relPatch),
		"copilot from an offset": func(t *testing.T) []model.Session {
			ss := vocabCopilot(relPatch)(t)
			path := ss[0].Path
			// Past session.start, the way an incremental index reads on.
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			off := int64(strings.Index(string(raw), "\n") + 1)
			out, err := ParseCopilotFileFromOffset(path, off)
			if err != nil {
				t.Fatal(err)
			}
			return out
		},
		"cline-sdk": func(t *testing.T) []model.Session {
			dir := filepath.Join(t.TempDir(), "1790877871095_abcdf")
			vocabWrite(t, filepath.Join(dir, "1790877871095_abcdf.json"), `{"session_id":"1790877871095_abcdf","cwd":"/tmp/proj"}`)
			p := vocabWrite(t, filepath.Join(dir, "1790877871095_abcdf.messages.json"), vocabJSON(map[string]any{"messages": []any{
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "c1", "name": "apply_patch", "input": map[string]any{"input": relPatch}}}},
			}}))
			return vocabParse(t, ParseClineFile, p)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ss := parse(t)
			for _, w := range relPatchWants {
				if !vocabHas(ss, w) {
					t.Errorf("missing %q; files %q, edits %q", w, vocabRoles(ss, RoleFiles), vocabRoles(ss, RoleEdit))
				}
			}
		})
	}
}

// Cline matches its markers as whole lines of three or more of a character —
// /^[-]{3,} SEARCH>?$/, /^[=]{3,}$/, /^[+]{3,} REPLACE>?$/ and Roo's < and >
// spellings — so a model's `--- SEARCH` is a block and an indented `=======`
// inside the replaced code is not a marker (#4504).
func TestClineDiffMarkersAsClineReadsThem(t *testing.T) {
	cases := []struct {
		name, diff        string
		replaced, written []string
	}{
		{"canonical", "------- SEARCH\na\n=======\nb\n+++++++ REPLACE", []string{"a"}, []string{"b"}},
		{"roo's form", "<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE", []string{"a"}, []string{"b"}},
		{"three and eight", "--- SEARCH\na\n===\nb\n++++++++ REPLACE", []string{"a"}, []string{"b"}},
		{"trailing >", "------- SEARCH>\na\n=======\nb\n+++++++ REPLACE>", []string{"a"}, []string{"b"}},
		{"two blocks", "------- SEARCH\na\n=======\nb\n+++++++ REPLACE\n\n------- SEARCH\nc\n=======\nd\n+++++++ REPLACE", []string{"a", "c"}, []string{"b", "d"}},
		{"crlf", "------- SEARCH\r\na\r\n=======\r\nb\r\n+++++++ REPLACE\r\n", []string{"a\r"}, []string{"b\r"}},
		{"indented rule in the code", "------- SEARCH\nx := 1\n    =======\ny := 2\n=======\nz := 3\n+++++++ REPLACE", []string{"x := 1\n    =======\ny := 2"}, []string{"z := 3"}},
		{"roo line numbers", "<<<<<<< SEARCH\n:start_line:3\n-------\na\n=======\nb\n>>>>>>> REPLACE", []string{"a"}, []string{"b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			replaced, written := rooDiffSides(c.diff, clineMarkers)
			if strings.Join(replaced, "|") != strings.Join(c.replaced, "|") || strings.Join(written, "|") != strings.Join(c.written, "|") {
				t.Errorf("sides = %q / %q, want %q / %q", replaced, written, c.replaced, c.written)
			}
		})
	}
}

// Roo's apply_diff takes exactly seven, so a `===` rule in the replaced text
// stays text there (#4504).
func TestRooDiffMarkersStayExact(t *testing.T) {
	replaced, written := rooDiffSides("<<<<<<< SEARCH\nTitle\n===\n=======\nHeading\n===\n>>>>>>> REPLACE", rooMarkers)
	if len(replaced) != 1 || replaced[0] != "Title\n===" || len(written) != 1 || written[0] != "Heading\n===" {
		t.Errorf("sides = %q / %q", replaced, written)
	}
}

// The v3 engine persists a tool_call line each time the action changes state
// — awaiting approval, running, then how it ended — under one id and one
// start time. Every line was read as the call made again, and a denied write
// as a write (#4506).
func TestKiroIDECallsOnceAndOnlyWhenRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "f045c81011ce49e2", "sess_00000000-0000-4000-8000-0000000000ab")
	line := func(id, name, status string, args any) string {
		return vocabJSON(map[string]any{"id": id + "-call", "timestamp": "2026-10-01T10:00:01.000Z", "payload": map[string]any{
			"type": "tool_call", "toolCallId": id, "toolName": name, "args": args, "status": status, "kind": "edit", "executionId": "e1", "actionType": name}})
	}
	edit := map[string]any{"path": "/tmp/proj/retry.go", "oldStr": oldLoop, "newStr": newLoop}
	denied := map[string]any{"path": "/tmp/proj/secret.go", "text": jitter}
	failed := map[string]any{"path": "/tmp/proj/failed.go", "oldStr": "func stale() {}", "newStr": "func fresh() { return nil }"}
	p := vocabWrite(t, filepath.Join(dir, "messages.jsonl"),
		`{"id":"u1","timestamp":"2026-10-01T10:00:00.000Z","payload":{"type":"user","content":"fix the retry loop"}}`,
		line("a1", "str_replace", "awaiting_approval", edit),
		line("a1", "str_replace", "executing", edit),
		line("a1", "str_replace", "completed", edit),
		line("a2", "fs_write", "awaiting_approval", denied),
		line("a2", "fs_write", "denied", denied),
		line("a3", "str_replace", "executing", failed),
		line("a3", "str_replace", "failed", failed),
	)
	ss := vocabParse(t, ParseKiroIDEFile, p)
	if got := vocabRoles(ss, RoleEdit); len(got) != 1 || got[0] != "/tmp/proj/retry.go\n"+oldLoop {
		t.Errorf("edit records = %q, want the one completed str_replace once", got)
	}
	if got := vocabRoles(ss, RoleWrote); len(got) != 1 || got[0] != WroteRecord("/tmp/proj/retry.go", newLoop) {
		t.Errorf("wrote records = %q, want the one completed str_replace once", got)
	}
	for _, f := range vocabRoles(ss, RoleFiles) {
		if strings.Contains(f, "secret.go") {
			t.Errorf("a denied write left a files record %q", f)
		}
	}
}

// grok-dev stores a call whose arguments were not valid JSON with input as
// the raw string, and names files relative to the session's cwd. The string
// cost every other call in its message; the relative path matched no file
// (#4498).
func TestGrokDBRawInputAndRelativePaths(t *testing.T) {
	msg := map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool-call", "toolCallId": "c0", "toolName": "bash", "input": `{"command": "go test`},
		map[string]any{"type": "tool-call", "toolCallId": "c1", "toolName": "edit_file", "input": map[string]any{"path": "retry.go", "old_string": oldLoop, "new_string": newLoop}},
	}}
	db := vocabSQL(t, `CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('gd2','w1','fix the retry loop','/tmp/proj','2026-09-20T10:00:00.000Z');
INSERT INTO messages VALUES ('gd2',0,'user','{"role":"user","content":"fix the retry loop"}','2026-09-20T10:00:00.000Z');
INSERT INTO messages VALUES ('gd2',1,'assistant',`+sqlQuote(vocabJSON(msg))+`,'2026-09-20T10:00:01.000Z');`)
	ss := vocabParse(t, func(db string) ([]model.Session, error) { return ParseGrokDBSince(db, time.Time{}) }, db)
	for _, w := range relPatchWants {
		if !vocabHas(ss, w) {
			t.Errorf("missing %q; files %q, edits %q", w, vocabRoles(ss, RoleFiles), vocabRoles(ss, RoleEdit))
		}
	}
}

// read_many_files takes paths relative to the project and directories beside
// files ("docs/"): a directory is not a file the session read, and a relative
// path is under the directory the session ran in (#4494).
func TestGeminiReadManyFilesDirectoriesAndRelativePaths(t *testing.T) {
	_, chats := geminiTree(t)
	vocabWrite(t, filepath.Join(filepath.Dir(chats), ".project_root"), "/tmp/proj")
	call := map[string]any{"id": "rm1", "name": "read_many_files", "status": "success",
		"args": map[string]any{"include": []any{"docs/", "src/a.go", "/abs/b.go", "src/**/*.go"}}}
	p := vocabWrite(t, filepath.Join(chats, "session-2026-10-02T10-00-sess-v-2.jsonl"),
		`{"sessionId":"sess-v-2","projectHash":"abc","startTime":"2026-10-02T10:00:00.000Z","lastUpdated":"2026-10-02T10:01:00.000Z","kind":"main"}`,
		`{"id":"u1","timestamp":"2026-10-02T10:00:01.000Z","type":"user","content":[{"text":"read the docs"}]}`,
		`{"id":"g1","timestamp":"2026-10-02T10:00:02.000Z","type":"gemini","content":"","model":"luna","toolCalls":[`+vocabJSON(call)+`]}`,
	)
	ss := vocabParse(t, ParseGeminiFile, p)
	if got := vocabRoles(ss, RoleFiles); len(got) != 1 || got[0] != "/tmp/proj/src/a.go\n/abs/b.go" {
		t.Errorf("files records = %q, want the two files, the relative one under the project", got)
	}
}

// The glob filter is opt-in per dialect: a list of plain paths keeps a Next.js
// route file named with brackets (#4494). Command Code opts in, since 1.74
// expands any entry with *, ?, [ or { as a glob (isGlobPattern, #4540).
func TestPathListKeepsBracketedFiles(t *testing.T) {
	got := toolPathStrings(map[string]any{"paths": []any{"/tmp/proj/app/[id]/page.tsx"}}, toolDialect{pathListKey: "paths"})
	if len(got) != 1 || got[0] != "/tmp/proj/app/[id]/page.tsx" {
		t.Errorf("paths = %q, want the bracketed route file", got)
	}
}
