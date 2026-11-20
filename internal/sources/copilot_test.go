package sources

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// writeCopilotAgentTranscript writes one of the extension's own transcripts,
// in the layout a newer Copilot Chat uses (#3637).
func writeCopilotAgentTranscript(t *testing.T, root, hash, id, workspace string, lines []string) string {
	t.Helper()
	dir := filepath.Join(root, "workspaceStorage", hash, "GitHub.copilot-chat", "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if workspace != "" {
		wp := filepath.Join(root, "workspaceStorage", hash, "workspace.json")
		if err := os.WriteFile(wp, []byte(workspace), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// On VS Code Server 1.137 there is no `chatSessions` directory at all and the
// history is in the extension's own transcripts, so the source found zero
// files; on desktop 1.136.1 both layouts sit side by side (#3637).
func TestCopilotAgentTranscriptIsReadAndSearchable(t *testing.T) {
	root := copilotChatRoot(t)
	p := writeCopilotAgentTranscript(t, root, "h1", "s-agent", `{"folder":"file:///work/projA"}`, []string{
		`{"type":"session.start","data":{"sessionId":"s-agent","version":1,"producer":"copilot-agent","copilotVersion":"0.62.0","vscodeVersion":"1.137.0","startTime":"2026-09-14T05:46:01.086Z"},"id":"e1","timestamp":"2026-09-14T05:46:01.086Z","parentId":null}`,
		`{"type":"user.message","data":{"content":"why does the packer manifest fail"},"id":"e2","timestamp":"2026-09-14T05:46:10.000Z","parentId":"e1"}`,
		`{"type":"assistant.turn_start","data":{},"id":"e3","timestamp":"2026-09-14T05:46:11.000Z"}`,
		`{"type":"assistant.message","data":{"messageId":"m1","content":"The manifest names a source that is not built.","toolRequests":[{"toolCallId":"c1","name":"read_file","arguments":"{\"filePath\":\"/work/projA/packer/manifest.json\"}","type":"function"}]},"id":"e4","timestamp":"2026-09-14T05:46:12.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"c2","toolName":"run_in_terminal","arguments":{"command":"packer build -only=amd64 ."}},"id":"e5","timestamp":"2026-09-14T05:46:13.000Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"c2","success":true},"id":"e6","timestamp":"2026-09-14T05:46:14.000Z"}`,
		`{"type":"assistant.turn_end","data":{},"id":"e7","timestamp":"2026-09-14T05:46:15.000Z"}`,
	})
	if KindForPath(p) != "copilot-chat" {
		t.Fatalf("KindForPath = %q, want copilot-chat", KindForPath(p))
	}
	files := CopilotChatSessionFiles()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("discovery = %v", files)
	}
	ss := LoadCopilotChat()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "s-agent" {
		t.Errorf("id = %q", s.ID)
	}
	if s.Project != "work/projA" {
		t.Errorf("project = %q, want the workspace.json folder", s.Project)
	}
	if s.Started.IsZero() {
		t.Error("session.start carried a start time and it was dropped")
	}
	roles := make([]string, 0, len(s.Messages))
	var joined string
	var joinedSb68 strings.Builder
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
		joinedSb68.WriteString(m.Role + ": " + m.Text + "\n")
	}
	joined += joinedSb68.String()
	for _, want := range []string{"why does the packer manifest fail", "names a source that is not built"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q is missing from the session:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "$ packer build -only=amd64 .") {
		t.Errorf("the command a tool call ran was not recorded as one:\n%s", joined)
	}
	if !strings.Contains(joined, "/work/projA/packer/manifest.json") {
		t.Errorf("the file a tool call touched was not recorded:\n%s", joined)
	}
	if strings.Count(joined, "user:") != 1 {
		t.Errorf("roles = %v", roles)
	}
}

// 11 of the reporter's 47 transcripts and all five on this machine hold no
// `user.message` at all: those are agent runs, and what the agent said and the
// files it touched are the only record of them.
func TestCopilotAgentTranscriptWithNoUserTurnIsStillIndexed(t *testing.T) {
	root := copilotChatRoot(t)
	writeCopilotAgentTranscript(t, root, "h2", "s-quiet", "", []string{
		`{"type":"session.start","data":{"sessionId":"s-quiet","startTime":"2026-09-06T15:14:41.310Z"},"id":"e1","timestamp":"2026-09-06T15:14:41.310Z"}`,
		`{"type":"assistant.message","data":{"messageId":"m1","content":"I will locate the manifest and check the validation failure.","toolRequests":[]},"id":"e2","timestamp":"2026-09-06T15:14:42.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"c1","toolName":"read_file","arguments":{"filePath":"/work/projB/main.go"}},"id":"e3","timestamp":"2026-09-06T15:14:43.000Z"}`,
	})
	ss := LoadCopilotChat()
	if len(ss) != 1 {
		t.Fatalf("an agent-only transcript was dropped: %d sessions", len(ss))
	}
	if ss[0].Title == "" {
		t.Error("a session with no user turn got no title")
	}
}

// `transcripts` is a generic directory name — this repository reads one for
// another harness — so the parent has to be the extension's own directory.
func TestAGenericTranscriptsDirectoryIsNotCopilotChats(t *testing.T) {
	root := copilotChatRoot(t)
	dir := filepath.Join(root, "workspaceStorage", "h3", "SomeOther.extension", "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "x.jsonl")
	if err := os.WriteFile(p, []byte(`{"type":"user.message","data":{"content":"hello"},"timestamp":"2026-09-14T05:46:10.000Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if copilotAgentTranscript(p) {
		t.Error("a transcripts directory of somebody else's was claimed")
	}
	if KindForPath(p) == "copilot-chat" {
		t.Error("KindForPath claimed it")
	}
	if files := CopilotChatSessionFiles(); len(files) != 0 {
		t.Errorf("discovery took it: %v", files)
	}
}

// The file grows while the chat runs, so the last line of a live session is
// half-written. That is one skipped line, not a dropped session.
func TestCopilotAgentTranscriptSurvivesAHalfWrittenLastLine(t *testing.T) {
	root := copilotChatRoot(t)
	writeCopilotAgentTranscript(t, root, "h4", "s-live", "", []string{
		`{"type":"session.start","data":{"sessionId":"s-live","startTime":"2026-09-14T05:46:01.086Z"},"id":"e1","timestamp":"2026-09-14T05:46:01.086Z"}`,
		`{"type":"user.message","data":{"content":"the seeder dies with SHARDMAP_NOT_FOUND"},"id":"e2","timestamp":"2026-09-14T05:46:10.000Z"}`,
		`{"type":"assistant.message","data":{"messageId":"m1","content":"It needs SHARD_SEED_PROFILE`,
	})
	ss := LoadCopilotChat()
	if len(ss) != 1 {
		t.Fatalf("a live session was dropped: %d", len(ss))
	}
	if !strings.Contains(ss[0].Messages[0].Text, "SHARDMAP_NOT_FOUND") {
		t.Errorf("the whole lines before it were lost: %+v", ss[0].Messages)
	}
}

// Discovery no longer walks the editor's User folder; it asks for the
// directories VS Code puts transcripts in. That is faster by 65x on a real
// machine and it is also narrower, so every layout it has to cover is named
// here — a shape this forgets is a store that silently stops being read.
func TestCopilotChatFindsEveryLayoutWithoutWalking(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", root)

	write := func(parts ...string) string {
		p := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"kind":0}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	want := []string{
		// A workspace's own sessions.
		write("workspaceStorage", "abc123", "chatSessions", "s1.jsonl"),
		// A window opened on no folder.
		write("globalStorage", "emptyWindowChatSessions", "s2.jsonl"),
		// And the same two again inside a named profile, which keeps its own
		// copy of both one level further down.
		write("profiles", "-1a2b3c", "workspaceStorage", "def456", "chatSessions", "s3.jsonl"),
		write("profiles", "-1a2b3c", "globalStorage", "emptyWindowChatSessions", "s4.jsonl"),
	}

	// Things that live in the same tree and are not transcripts. The walk this
	// replaces read all of them.
	write("History", "1a2b3c", "entries.json")
	write("workspaceStorage", "abc123", "state.vscdb")
	write("globalStorage", "some.extension", "cache.json")
	write("workspaceStorage", "abc123", "chatSessions", "notes.txt")

	got := CopilotChatSessionFiles()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("discovery found\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A root in none of those shapes still gets walked, so a layout nobody has seen
// is slow rather than invisible.
func TestCopilotChatWalksARootItDoesNotRecognise(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", root)
	odd := filepath.Join(root, "somewhere", "else", "deeper", "chatSessions")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(odd, "s1.jsonl")
	if err := os.WriteFile(p, []byte(`{"kind":0}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CopilotChatSessionFiles()
	if len(got) != 1 || got[0] != p {
		t.Fatalf("an unrecognised layout was not walked: %v", got)
	}

	// And once the root looks like VS Code's, the targeted read is used — which
	// is the trade this makes, and the reason the shapes above are pinned.
	if err := os.MkdirAll(filepath.Join(root, "workspaceStorage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CopilotChatSessionFiles(); len(got) != 0 {
		t.Fatalf("a recognised root still walked the whole tree: %v", got)
	}
}

// copilotChatRequestWithCalls is one chatSessions request whose edits are in
// result.metadata, the way VS Code 1.136 stores agent mode: each call's
// arguments a JSON string, each result keyed by the call's id.
func copilotChatRequestWithCalls(t *testing.T, calls []map[string]any, results map[string]any) map[string]any {
	t.Helper()
	tc := make([]any, 0, len(calls))
	for _, c := range calls {
		args, err := json.Marshal(c["args"])
		if err != nil {
			t.Fatal(err)
		}
		tc = append(tc, map[string]any{"name": c["name"], "id": c["id"], "arguments": string(args)})
	}
	return map[string]any{
		"timestamp":         1763727104742,
		"message":           map[string]any{"text": "give the pool an acquire timeout"},
		"response":          []any{map[string]any{"value": "Done."}},
		"responseTimestamp": 1763727400000,
		"result": map[string]any{"metadata": map[string]any{
			"toolCallRounds":  []any{map[string]any{"toolCalls": tc}},
			"toolCallResults": results,
		}},
	}
}

func copilotChatRecordsOf(t *testing.T, path, role string) []string {
	t.Helper()
	ss, err := ParseCopilotChatFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var out []string
	for _, m := range ss[0].Messages {
		if m.Role == role {
			out = append(out, m.Text)
		}
	}
	sort.Strings(out)
	return out
}

// The replaced side of a Copilot Chat edit is in the call's arguments, not in
// the textEditGroup beside it, and the reader took only the latter: 638 spans
// on a local store, none indexed (#595). Each edit tool is read, and a call the
// extension refused leaves nothing.
func TestCopilotChatReadsTheReplacedSideFromToolCallRounds(t *testing.T) {
	const pool = "/w/app/internal/pool/pool.go"
	const cfg = "/w/app/internal/pool/config.go"
	calls := []map[string]any{
		{"name": "replace_string_in_file", "id": "c1", "args": map[string]any{
			"filePath": pool, "oldString": "func (p *Pool) Acquire() (*Conn, error) {", "newString": "func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {"}},
		{"name": "multi_replace_string_in_file", "id": "c2", "args": map[string]any{
			"explanation": "two constants",
			"replacements": []any{
				map[string]any{"filePath": cfg, "oldString": "const acquireTimeout = 0", "newString": "const acquireTimeout = 5 * time.Second"},
				map[string]any{"filePath": "file:///w/app/internal/pool/limits.go", "oldString": "const maxConns = 4", "newString": "const maxConns = 16"},
			}}},
		{"name": "apply_patch", "id": "c3", "args": map[string]any{
			"explanation": "drop the busy loop",
			"input":       "*** Begin Patch\n*** Update File: " + pool + "\n@@\n-\tfor !p.ready() { runtime.Gosched() }\n+\t<-p.readyCh\n*** End Patch"}},
		{"name": "apply_patch", "id": "c4", "args": map[string]any{
			"input": "*** Begin Patch\n*** Update File: " + pool + "\n@@\n-\tthis patch never applied\n+\tso nothing changed\n*** End Patch"}},
		{"name": "create_file", "id": "c5", "args": map[string]any{
			"filePath": "/w/app/internal/pool/doc.go", "content": "// Package pool hands out connections with a deadline.\npackage pool\n"}},
		{"name": "read_file", "id": "c6", "args": map[string]any{"filePath": pool, "startLine": 1, "endLine": 40}},
	}
	results := map[string]any{
		"c1": map[string]any{"content": []any{map[string]any{"value": "The following files were successfully edited"}}},
		"c4": map[string]any{"content": []any{map[string]any{"value": "Applying patch failed with error: Invalid context at character 12"}}},
	}
	state := map[string]any{
		"version": 3, "sessionId": "s", "creationDate": 1763727100000,
		"requests": []any{copilotChatRequestWithCalls(t, calls, results)},
	}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "s.json")
	if err = os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got := copilotChatRecordsOf(t, p, RoleEdit)
	want := []string{
		pool + "\n\tfor !p.ready() { runtime.Gosched() }",
		pool + "\nfunc (p *Pool) Acquire() (*Conn, error) {",
		cfg + "\nconst acquireTimeout = 0",
		"/w/app/internal/pool/limits.go\nconst maxConns = 4",
	}
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("edits =\n%q\nwant\n%q", got, want)
	}

	// The same request through the delta log VS Code writes beside it.
	line, err := json.Marshal(map[string]any{"kind": 0, "v": state})
	if err != nil {
		t.Fatal(err)
	}
	pl := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(pl, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := copilotChatRecordsOf(t, pl, RoleEdit); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("delta log edits =\n%q\nwant\n%q", got, want)
	}

	t.Setenv("DEJA_INDEX_EDITS", "0")
	if got := copilotChatRecordsOf(t, p, RoleEdit); len(got) != 0 {
		t.Fatalf("edits survived their switch: %q", got)
	}
}

// The agent transcripts hold the same calls under tool.execution_start, with
// nothing beside them for the written side, so both sides come from the call.
// A call that completed without success changed nothing (#595).
func TestCopilotAgentTranscriptRecordsBothSidesOfAnEdit(t *testing.T) {
	root := copilotChatRoot(t)
	const pool = "/work/projA/internal/pool/pool.go"
	start := func(id, name string, args map[string]any) string {
		b, err := json.Marshal(map[string]any{"type": "tool.execution_start", "timestamp": "2026-09-14T05:46:13.000Z",
			"data": map[string]any{"toolCallId": id, "toolName": name, "arguments": args}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	p := writeCopilotAgentTranscript(t, root, "h1", "s-agent", `{"folder":"file:///work/projA"}`, []string{
		`{"type":"session.start","data":{"sessionId":"s-agent","startTime":"2026-09-14T05:46:01.086Z"},"timestamp":"2026-09-14T05:46:01.086Z"}`,
		`{"type":"user.message","data":{"content":"give the pool an acquire timeout"},"timestamp":"2026-09-14T05:46:10.000Z"}`,
		start("c1", "replace_string_in_file", map[string]any{"filePath": pool, "oldString": "func (p *Pool) Acquire() (*Conn, error) {", "newString": "func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {"}),
		`{"type":"tool.execution_complete","data":{"toolCallId":"c1","success":true},"timestamp":"2026-09-14T05:46:14.000Z"}`,
		start("c2", "replace_string_in_file", map[string]any{"filePath": pool, "oldString": "a string the file never held", "newString": "and so was never written anywhere"}),
		`{"type":"tool.execution_complete","data":{"toolCallId":"c2","success":false},"timestamp":"2026-09-14T05:46:15.000Z"}`,
	})
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var edits, wrote, files []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleEdit:
			edits = append(edits, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		}
	}
	if strings.Join(edits, "|") != pool+"\nfunc (p *Pool) Acquire() (*Conn, error) {" {
		t.Fatalf("edits = %q", edits)
	}
	h, _ := HashWrittenLine("func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {")
	if len(wrote) != 1 {
		t.Fatalf("wrote = %q, want the successful call's one record", wrote)
	}
	if path, has := WroteRecordHas(wrote[0], h); !has || path != pool {
		t.Fatalf("wrote = %q, want the new signature under %s", wrote, pool)
	}
	if strings.Join(files, "|") != pool {
		t.Fatalf("files = %q", files)
	}
}

// insert_edit_into_file {explanation, filePath, code} sends the new text with
// "...existing code..." for the lines it kept; the transcript had its path and
// nothing for blame to match (#595).
func TestCopilotAgentInsertEditRecordsTheWrittenSide(t *testing.T) {
	root := copilotChatRoot(t)
	const pool = "/work/projA/internal/pool/pool.go"
	const added = "deadline, cancel := context.WithTimeout(ctx, p.acquireTimeout)"
	start := func(id string, args map[string]any) string {
		b, err := json.Marshal(map[string]any{"type": "tool.execution_start", "timestamp": "2026-09-14T05:46:13.000Z",
			"data": map[string]any{"toolCallId": id, "toolName": "insert_edit_into_file", "arguments": args}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	p := writeCopilotAgentTranscript(t, root, "h1", "s-insert", `{"folder":"file:///work/projA"}`, []string{
		`{"type":"session.start","data":{"sessionId":"s-insert","startTime":"2026-09-14T05:46:01.086Z"},"timestamp":"2026-09-14T05:46:01.086Z"}`,
		`{"type":"user.message","data":{"content":"give the pool an acquire timeout"},"timestamp":"2026-09-14T05:46:10.000Z"}`,
		start("c1", map[string]any{"explanation": "Bound Acquire by a timeout.", "filePath": pool,
			"code": "// ...existing code above Acquire...\nfunc (p *Pool) Acquire(ctx context.Context) (*Conn, error) {\n\t" + added + "\n// ...existing code..."}),
		`{"type":"tool.execution_complete","data":{"toolCallId":"c1","success":true},"timestamp":"2026-09-14T05:46:14.000Z"}`,
		start("c2", map[string]any{"explanation": "x", "filePath": pool, "code": "refused = a line the extension never wrote anywhere"}),
		`{"type":"tool.execution_complete","data":{"toolCallId":"c2","success":false},"timestamp":"2026-09-14T05:46:15.000Z"}`,
	})
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	wrote := copilotChatRecordsOf(t, p, RoleWrote)
	if len(wrote) != 1 {
		t.Fatalf("wrote = %q, want the successful call's one record", wrote)
	}
	h, _ := HashWrittenLine(added)
	if path, has := WroteRecordHas(wrote[0], h); !has || path != pool {
		t.Fatalf("wrote = %q, want the added line under %s", wrote, pool)
	}
	elided, _ := HashWrittenLine("// ...existing code above Acquire...")
	if _, has := WroteRecordHas(wrote[0], elided); has {
		t.Fatalf("the elision marker was hashed as a written line: %q", wrote)
	}
	if got := copilotChatRecordsOf(t, p, RoleEdit); len(got) != 0 {
		t.Fatalf("an insert edit has no replaced side, got %q", got)
	}
}

// VS Code keeps which files a chat edited beside the transcript, and deja read
// none of it: a `files` record appeared only when the conversation happened to
// type a path. In practice, 48 of these state files, their
// entries carrying 145 resources between them (#3381).
func TestACopilotChatNamesTheFilesItEdited(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "workspaceStorage", "abc123")
	chats := filepath.Join(ws, "chatSessions")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "3f2b9c11-0000-4000-8000-abcdefabcdef"
	transcript := `{"version":3,"sessionId":"` + id + `","creationDate":1767225600000,` +
		`"requests":[{"message":{"text":"make the retry budget configurable"},` +
		`"response":[{"value":"done"}]}]}`
	path := filepath.Join(chats, id+".json")
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	edits := filepath.Join(ws, "chatEditingSessions", id)
	if err := os.MkdirAll(edits, 0o755); err != nil {
		t.Fatal(err)
	}
	// The shape this machine's own files carry: workingSet empty, the entries
	// holding the resources.
	state := `{"version":2,"sessionId":"` + id + `","recentSnapshot":{"workingSet":[],` +
		`"entries":[{"resource":{"scheme":"file","path":"/work/app/retry.go"}},` +
		`{"resource":"file:///work/app/retry_test.go"}]}}`
	if err := os.WriteFile(filepath.Join(edits, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCopilotChatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the one transcript", len(ss))
	}
	var files string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = m.Text
		}
	}
	for _, want := range []string{"/work/app/retry.go", "/work/app/retry_test.go"} {
		if !strings.Contains(files, want) {
			t.Errorf("the files record does not name %s: %q", want, files)
		}
	}
}

// And a chat with no such state file is what it was: no record invented.
func TestACopilotChatWithoutEditStateIsUnchanged(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "workspaceStorage", "abc123")
	chats := filepath.Join(ws, "chatSessions")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "aaaaaaaa-0000-4000-8000-aaaaaaaaaaaa"
	path := filepath.Join(chats, id+".json")
	transcript := `{"version":3,"sessionId":"` + id + `","creationDate":1767225600000,` +
		`"requests":[{"message":{"text":"why does the retry budget reset"},"response":[{"value":"it does not"}]}]}`
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			t.Errorf("a files record appeared with no edit state: %q", m.Text)
		}
	}
}

const copilotChatEditedFilesTestID = "bbbbbbbb-0000-4000-8000-bbbbbbbbbbbb"

func copilotChatEditedFilesText(t *testing.T, state string) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "workspaceStorage", "abc123")
	chats := filepath.Join(ws, "chatSessions")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"version":3,"sessionId":"` + copilotChatEditedFilesTestID +
		`","creationDate":1767225600000,"requests":[{"message":{"text":"edit the files"},` +
		`"response":[{"value":"done"}]}]}`
	path := filepath.Join(chats, copilotChatEditedFilesTestID+".json")
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	edits := filepath.Join(ws, "chatEditingSessions", copilotChatEditedFilesTestID)
	if err := os.MkdirAll(edits, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(edits, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCopilotChatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the one transcript", len(ss))
	}
	var records []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			records = append(records, m.Text)
		}
	}
	if len(records) == 0 {
		return ""
	}
	if len(records) != 1 {
		t.Fatalf("files records = %d, want one: %q", len(records), records)
	}
	return records[0]
}

// VS Code writes string resources with URI.toString(), which percent-encodes
// their paths, while UriComponents paths are already decoded.
func TestACopilotChatDecodesThePercentEncodedResource(t *testing.T) {
	state := `{"version":2,"sessionId":"` + copilotChatEditedFilesTestID +
		`","recentSnapshot":{"entries":[` +
		`{"resource":"file:///c%3A/Users/me/my%20app/main.go"},` +
		`{"resource":{"scheme":"file","path":"/c:/Users/me/my app/other.go"}}]}}`
	files := copilotChatEditedFilesText(t, state)

	for _, want := range []string{
		"/c:/Users/me/my app/main.go",
		"/c:/Users/me/my app/other.go",
	} {
		if !strings.Contains(files, want) {
			t.Errorf("the decoded files record does not name %s: %q", want, files)
		}
	}
	for _, escaped := range []string{"%3A", "%20"} {
		if strings.Contains(files, escaped) {
			t.Errorf("the files record kept %s instead of decoding it: %q", escaped, files)
		}
	}
}

// VS Code's older snapshot stored workingSet as ResourceMapDTO pairs, while
// its reader still took the edited files only from entries.
func TestACopilotChatReadsTheOlderWorkingSetShape(t *testing.T) {
	state := `{"version":1,"sessionId":"` + copilotChatEditedFilesTestID +
		`","recentSnapshot":{"requestId":"r1",` +
		`"workingSet":[["file:///work/app/a.go",{"state":0}]],` +
		`"entries":[{"resource":"file:///work/app/b.go"}]}}`
	files := copilotChatEditedFilesText(t, state)

	if !strings.Contains(files, "/work/app/b.go") {
		t.Errorf("the files record does not name the snapshot entry: %q", files)
	}
	if strings.Contains(files, "/work/app/a.go") {
		t.Errorf("the files record misattributes a working-set key as edited: %q", files)
	}
}

// The sidecar can carry an encoded newline inside one URI, while RoleFiles
// uses literal newlines to separate resources.
func TestACopilotChatDropsAResourceThatDecodesToANewline(t *testing.T) {
	state := `{"version":2,"sessionId":"` + copilotChatEditedFilesTestID +
		`","recentSnapshot":{"entries":[` +
		`{"resource":"file:///work/app/x%0Ay.go"},` +
		`{"resource":"file:///work/app/trailing.go%0A"},` +
		`{"resource":"file:///work/app/cr.go%0D"},` +
		`{"resource":"file:///work/app/ok.go"}]}}`
	files := copilotChatEditedFilesText(t, state)
	lines := strings.Split(files, "\n")

	if len(lines) != 1 || lines[0] != "/work/app/ok.go" {
		t.Fatalf("files lines = %q, want only /work/app/ok.go", lines)
	}
}

// URI.toString() keeps a file URI's server in the authority rather than in
// the decoded path.
func TestACopilotChatKeepsAUNCResourceAuthority(t *testing.T) {
	state := `{"version":2,"sessionId":"` + copilotChatEditedFilesTestID +
		`","recentSnapshot":{"entries":[` +
		`{"resource":"file://server/share/proj/f.go"}]}}`
	files := copilotChatEditedFilesText(t, state)

	if files != "//server/share/proj/f.go" {
		t.Fatalf("files = %q, want //server/share/proj/f.go", files)
	}
}

// VS Code writes a workspace folder as a URI, and a folder whose name has a
// space in it arrives percent-encoded. Reading it raw named the project
// `my%20app`, which is a project nobody searches for and a project filter
// nothing matches — the shape a contributor hit on `C:\Users\me\my app`
// (#3498, #3505).
func TestCopilotChatDecodesAWorkspaceURI(t *testing.T) {
	for _, tc := range []struct {
		name, uri, want string
	}{
		{name: "a space", uri: "file:///tmp/my%20app", want: "tmp/my app"},
		{name: "parentheses and a dot", uri: "file:///tmp/my%20app%20(v2.1)", want: "tmp/my app (v2.1)"},
		{name: "non-ASCII", uri: "file:///tmp/%D0%BF%D1%80%D0%BE%D0%B5%D0%BA%D1%82", want: "tmp/проект"},
		{name: "nothing to decode", uri: "file:///tmp/plain", want: "tmp/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := copilotChatProjectFromURI(tc.uri); got != tc.want {
				t.Fatalf("project = %q, want %q", got, tc.want)
			}
		})
	}
}

// The same through the file the reader actually opens, so the decode is
// pinned where a session gets its project rather than only in the helper.
func TestACopilotChatSessionUnderAnEncodedFolderKeepsItsProject(t *testing.T) {
	hermeticSourcesEnv(t)
	ws := filepath.Join(t.TempDir(), "workspaceStorage", "a1b2c3d4e5f6")
	if err := os.MkdirAll(filepath.Join(ws, "chatSessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "workspace.json"),
		[]byte(`{"folder":"file:///tmp/my%20app%20(v2.1)"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(ws, "chatSessions", "3b7e1f1b-0000-4000-8000-000000000000.jsonl")
	body := `{"kind":0,"v":{"version":3,"sessionId":"3b7e1f1b-0000-4000-8000-000000000000","creationDate":1763727100000,"customTitle":"fix the retry loop","requests":[],"workingDirectory":"file:///tmp/my%20app%20(v2.1)"}}
{"kind":2,"k":["requests"],"v":[{"requestId":"request_1","timestamp":1763727104742,"message":{"text":"why does the retry loop spin"},"response":[{"value":"because the budget resets on every attempt"}]}]}
`
	if err := os.WriteFile(session, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(session)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	if ss[0].Project != "tmp/my app (v2.1)" {
		t.Fatalf("project = %q — the folder URI was not decoded", ss[0].Project)
	}
}

func copilotChatHostTextSession(t *testing.T, body, ext string) model.Session {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session"+ext)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseCopilotChatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1 (%#v)", len(sessions), sessions)
	}
	return sessions[0]
}

func copilotChatRoleTexts(messages []model.Message, role string) []string {
	var texts []string
	for _, message := range messages {
		if message.Role == role {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

func TestCopilotChatDoesNotCallVSCodeRequestTextThePersons(t *testing.T) {
	tests := []struct {
		name     string
		fields   string
		wantUser bool
	}{
		{
			name:   "system initiated",
			fields: `,"isSystemInitiated":true`,
		},
		{
			name:   "confirmation",
			fields: `,"confirmation":"Accept"`,
		},
		{
			name:     "ordinary request",
			wantUser: true,
		},
		{
			name:     "false and empty",
			fields:   `,"isSystemInitiated":false,"confirmation":""`,
			wantUser: true,
		},
		{
			name:     "null markers",
			fields:   `,"isSystemInitiated":null,"confirmation":null`,
			wantUser: true,
		},
		{
			name:     "wrong marker types",
			fields:   `,"isSystemInitiated":"true","confirmation":true`,
			wantUser: true,
		},
		{
			name:     "whitespace confirmation",
			fields:   `,"confirmation":"  "`,
			wantUser: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"version":3,"sessionId":"flat","creationDate":1763727100000,"requests":[{` +
				`"timestamp":1763727104742` + tc.fields + `,` +
				`"message":{"text":"request text"},` +
				`"response":[{"value":"assistant reply"}],` +
				`"responseTimestamp":1763727400000}]}`

			session := copilotChatHostTextSession(t, body, ".json")
			messages := session.Messages
			users := copilotChatRoleTexts(messages, "user")
			if tc.wantUser {
				if len(users) != 1 || users[0] != "request text" {
					t.Fatalf("users = %#v, want the ordinary request", users)
				}
			} else if len(users) != 0 {
				t.Fatalf("users = %#v, want none", users)
			}
			assistants := copilotChatRoleTexts(messages, "assistant")
			if len(assistants) != 1 || assistants[0] != "assistant reply" {
				t.Fatalf("assistants = %#v, want the response", assistants)
			}
			if got := session.Started.UnixMilli(); got != 1763727100000 {
				t.Fatalf("started = %d, want 1763727100000", got)
			}
			if got := session.Updated.UnixMilli(); got != 1763727400000 {
				t.Fatalf("updated = %d, want 1763727400000", got)
			}
		})
	}
}

func TestCopilotChatReplaysAHostMarkerBeforeIndexing(t *testing.T) {
	body := `{"kind":0,"v":{"version":3,"sessionId":"replayed","creationDate":1763727100000,` +
		`"requests":[{"timestamp":1763727104742,"message":{"text":"host notification"},` +
		`"response":[{"value":"work completed"}],"responseTimestamp":1763727400000}]}}` + "\n" +
		`{"kind":1,"k":["requests",0,"isSystemInitiated"],"v":true}` + "\n"

	messages := copilotChatHostTextSession(t, body, ".jsonl").Messages
	if users := copilotChatRoleTexts(messages, "user"); len(users) != 0 {
		t.Fatalf("users = %#v, want none after replayed marker", users)
	}
	assistants := copilotChatRoleTexts(messages, "assistant")
	if len(assistants) != 1 || assistants[0] != "work completed" {
		t.Fatalf("assistants = %#v, want the response", assistants)
	}
}

func TestCopilotChatHostRequestKeepsCommandWork(t *testing.T) {
	t.Setenv("DEJA_INDEX_COMMANDS", "1")
	body := `{"version":3,"sessionId":"command","creationDate":1763727100000,"requests":[{` +
		`"timestamp":1763727104742,"isSystemInitiated":true,` +
		`"message":{"text":"terminal completion notification"},` +
		`"response":[{"kind":"toolInvocationSerialized","toolId":"runInTerminal",` +
		`"toolSpecificData":{"kind":"terminal","commandLine":{"original":"go test ./..."}}}],` +
		`"responseTimestamp":1763727400000}]}`

	messages := copilotChatHostTextSession(t, body, ".json").Messages
	if users := copilotChatRoleTexts(messages, "user"); len(users) != 0 {
		t.Fatalf("users = %#v, want none", users)
	}
	commands := copilotChatRoleTexts(messages, RoleCommand)
	if len(commands) != 1 || commands[0] != "$ go test ./..." {
		t.Fatalf("commands = %#v, want the terminal work record", commands)
	}
}

// A confirmation can arrive the same way, so the guard has to see it after the
// log is replayed rather than only on the entry the log opened with.
func TestCopilotChatReplaysAConfirmationBeforeIndexing(t *testing.T) {
	body := `{"kind":0,"v":{"version":3,"sessionId":"confirmed","creationDate":1763727100000,` +
		`"requests":[{"timestamp":1763727104742,"message":{"text":"Accept: \"Run go test?\""},` +
		`"response":[{"value":"ran it"}],"responseTimestamp":1763727400000}]}}` + "\n" +
		`{"kind":1,"k":["requests",0,"confirmation"],"v":"Accept"}` + "\n"

	messages := copilotChatHostTextSession(t, body, ".jsonl").Messages
	if users := copilotChatRoleTexts(messages, "user"); len(users) != 0 {
		t.Fatalf("users = %#v, want none after the replayed confirmation", users)
	}
	assistants := copilotChatRoleTexts(messages, "assistant")
	if len(assistants) != 1 || assistants[0] != "ran it" {
		t.Fatalf("assistants = %#v, want the response", assistants)
	}
}

// Dropping the text does not drop the turn. A background completion is when the
// session was last active, so it still has to move Updated: the second request
// here carries no response, so the only thing that can advance the session is
// the request itself.
func TestCopilotChatHostRequestStillTimesTheSession(t *testing.T) {
	body := `{"version":3,"sessionId":"timing","creationDate":1763727100000,"requests":[` +
		`{"timestamp":1763727104742,"message":{"text":"typed question"},` +
		`"response":[{"value":"answer"}],"responseTimestamp":1763727200000},` +
		`{"timestamp":1763727900000,"isSystemInitiated":true,` +
		`"message":{"text":"terminal finished"}}]}`

	session := copilotChatHostTextSession(t, body, ".json")
	users := copilotChatRoleTexts(session.Messages, "user")
	if len(users) != 1 || users[0] != "typed question" {
		t.Fatalf("users = %#v, want only the typed one", users)
	}
	if got := session.Updated.UnixMilli(); got != 1763727900000 {
		t.Fatalf("updated = %d, want the host turn at 1763727900000", got)
	}
}

func copilotChatAssistantText(t *testing.T, response string) (string, []string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{"version":3,"sessionId":"s","creationDate":1763727100000,"requests":[{` +
		`"timestamp":1763727104742,"message":{"text":"why does the price come back empty"},` +
		`"response":` + response + `,"responseTimestamp":1763727400000}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	var assistant, files []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case "assistant":
			assistant = append(assistant, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		}
	}
	return strings.Join(assistant, "\n"), files
}

// VS Code draws an inlineReference part as the file's or symbol's name in the
// middle of the reply. Dropped from the text, the sentence lost its subject —
// "The bug is in , line 40:  swallows the error." — and the symbol could not
// be searched for (#4589).
func TestCopilotChatInlineReferenceKeepsItsName(t *testing.T) {
	got, files := copilotChatAssistantText(t, `[
		{"value":"The bug is in "},
		{"kind":"inlineReference","inlineReference":{"$mid":1,"fsPath":"/tmp/proj/retry.go","path":"/tmp/proj/retry.go","scheme":"file"}},
		{"value":", line 40: "},
		{"kind":"inlineReference","inlineReference":{"name":"fetchPrice","kind":12,"location":{"uri":{"$mid":1,"path":"/tmp/proj/price.go","scheme":"file"},"range":{"startLineNumber":175,"startColumn":1,"endLineNumber":175,"endColumn":10}}}},
		{"value":" swallows the error."},
		{"kind":"inlineReference","inlineReference":{"$mid":1,"path":"/tmp/proj/README.md","scheme":"file"},"name":"the readme"}
	]`)
	if want := "The bug is in retry.go, line 40: fetchPrice swallows the error.the readme"; got != want {
		t.Errorf("assistant text = %q, want %q", got, want)
	}
	// The file records stay as they were.
	if strings.Join(files, ",") != "/tmp/proj/retry.go,/tmp/proj/price.go,/tmp/proj/README.md" {
		t.Errorf("files = %v", files)
	}
}

// Agent mode writes an edit as an empty code fence around a codeblockUri and
// the edit group, and VS Code draws a pill with the file's name there. Read as
// text, the reply had an empty fence in the middle of it (#4590). A fence with
// code in it stays as it is.
func TestCopilotChatAgentEditNamesTheFileInsteadOfAnEmptyFence(t *testing.T) {
	uri := `{"$mid":1,"fsPath":"/tmp/proj/retry.go","path":"/tmp/proj/retry.go","scheme":"file"}`
	got, files := copilotChatAssistantText(t, `[
		{"value":"Changing the cap:"},
		{"value":"\n`+"```"+`\n"},
		{"kind":"codeblockUri","uri":`+uri+`,"isEdit":true},
		{"kind":"textEditGroup","uri":`+uri+`,"edits":[[{"text":"for i := 0; i < 5; i++ {","range":{"startLineNumber":40,"startColumn":1,"endLineNumber":40,"endColumn":30}}],[]],"done":true},
		{"value":"\n`+"```"+`\n"},
		{"value":"The loop now stops after five attempts."}
	]`)
	if want := "Changing the cap:\nretry.go\nThe loop now stops after five attempts."; got != want {
		t.Errorf("assistant text = %q, want %q", got, want)
	}
	if len(files) == 0 || files[0] != "/tmp/proj/retry.go" {
		t.Errorf("files = %v", files)
	}

	got, _ = copilotChatAssistantText(t, `[
		{"value":"Like this:\n`+"```"+`go\n"},
		{"kind":"codeblockUri","uri":`+uri+`},
		{"value":"for i := 0; i < 5; i++ {\n`+"```"+`\n"}
	]`)
	if want := "Like this:\n```go\nfor i := 0; i < 5; i++ {\n```"; got != want {
		t.Errorf("a code block with code in it changed: %q, want %q", got, want)
	}
}

// A reference to the workspace root or a URI string with an escaped space
// reads as a name, not "/" or "%20".
func TestCopilotChatInlineReferenceOddPaths(t *testing.T) {
	got, _ := copilotChatAssistantText(t, `[
		{"value":"see "},
		{"kind":"inlineReference","inlineReference":{"$mid":1,"path":"/","scheme":"file"}},
		{"value":"and "},
		{"kind":"inlineReference","inlineReference":"file:///tmp/proj/my%20notes.md"}
	]`)
	if want := "see and my notes.md"; got != want {
		t.Errorf("assistant text = %q, want %q", got, want)
	}
}

// A URI string is named by its path, not its scheme: the workspace root is
// not "file:", and an untitled buffer is "Untitled-1".
func TestCopilotChatInlineReferenceURIScheme(t *testing.T) {
	got, _ := copilotChatAssistantText(t, `[
		{"value":"see "},
		{"kind":"inlineReference","inlineReference":"file:///"},
		{"value":"and "},
		{"kind":"inlineReference","inlineReference":"untitled:Untitled-1"}
	]`)
	if want := "see and Untitled-1"; got != want {
		t.Errorf("assistant text = %q, want %q", got, want)
	}
}

func copilotChatRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", root)
	return root
}

func writeCopilotChatSession(t *testing.T, root, hash, id, workspace, body string) string {
	t.Helper()
	dir := filepath.Join(root, "workspaceStorage", hash, "chatSessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if workspace != "" {
		wp := filepath.Join(root, "workspaceStorage", hash, "workspace.json")
		if err := os.WriteFile(wp, []byte(workspace), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeCopilotChatJSONL(t *testing.T, dir string, lines []string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func copilotChatReq(id, user, assistant string, ts, rts int64) string {
	return `{"requestId":"` + id + `","timestamp":` + itoa64(ts) + `,"message":{"text":"` + user + `"},"response":[{"value":"` + assistant + `"}],"responseTimestamp":` + itoa64(rts) + `}`
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	u := uint64(n) //nolint:gosec // the test passes non-negative values
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}

func TestParseCopilotChatJSONLReplay(t *testing.T) {
	root := t.TempDir()
	ts, rts := int64(1763727104742), int64(1763727400000)
	req := copilotChatReq("r1", "hello", "world", ts, rts)
	initial := `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"customTitle":"t1","requests":[]}}`

	tests := []struct {
		name string
		body string
		want int
		id   string
		user string
	}{
		{
			name: "kind0-then-push",
			body: initial + "\n" + `{"kind":2,"k":["requests"],"v":[` + req + `]}` + "\n",
			want: 1, id: "s1", user: "hello",
		},
		{
			name: "kind1-set-title",
			body: `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[` + req + `]}}` + "\n" +
				`{"kind":1,"k":["customTitle"],"v":"from set"}` + "\n",
			want: 1, id: "s1", user: "hello",
		},
		{
			name: "kind3-delete-unused-field",
			body: `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[` + req + `],"pendingRequests":true}}` + "\n" +
				`{"kind":3,"k":["pendingRequests"]}` + "\n",
			want: 1, id: "s1", user: "hello",
		},
		{
			name: "later-0-resets",
			body: `{"kind":0,"v":{"version":3,"sessionId":"old","creationDate":1763727100000,"requests":[` + req + `]}}` + "\n" +
				`{"kind":0,"v":{"version":3,"sessionId":"new","creationDate":1763727100000,"requests":[` + copilotChatReq("r2", "after", "reset", ts, rts) + `]}}` + "\n",
			want: 1, id: "new", user: "after",
		},
		{
			name: "kind1-before-0",
			body: `{"kind":1,"k":["customTitle"],"v":"nope"}` + "\n" + initial + "\n",
			want: 0,
		},
		{
			name: "malformed-line-drops",
			body: initial + "\n" + `{not json` + "\n" + `{"kind":2,"k":["requests"],"v":[` + req + `]}` + "\n",
			want: 0,
		},
		{
			name: "unknown-kind-drops",
			body: initial + "\n" + `{"kind":9,"v":1}` + "\n",
			want: 0,
		},
		{
			name: "empty-file",
			body: "",
			want: 0,
		},
		{
			name: "whitespace-only",
			body: "\n\n  \n",
			want: 0,
		},
		{
			name: "push-i-pads",
			body: initial + "\n" + `{"kind":2,"k":["requests"],"v":[` + req + `],"i":2}` + "\n",
			want: 1, id: "s1", user: "hello",
		},
		{
			name: "push-v-two-items",
			body: initial + "\n" + `{"kind":2,"k":["requests"],"v":[` +
				copilotChatReq("a", "one", "A", ts, rts) + `,` +
				copilotChatReq("b", "two", "B", ts+1, rts+1) + `]}` + "\n",
			want: 1, id: "s1", user: "one",
		},
		{
			name: "truncate-push",
			body: `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[` +
				copilotChatReq("a", "keep", "A", ts, rts) + `,` +
				copilotChatReq("b", "drop", "B", ts, rts) + `]}}` + "\n" +
				`{"kind":2,"k":["requests"],"v":[` + copilotChatReq("c", "new", "C", ts, rts) + `],"i":1}` + "\n",
			want: 1, id: "s1", user: "keep",
		},
		{
			name: "missing-parent-drops",
			body: initial + "\n" + `{"kind":1,"k":["requests",0,"message"],"v":{"text":"x"}}` + "\n",
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(root, tc.name+".jsonl")
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			ss, err := ParseCopilotChatFile(p)
			if err != nil {
				t.Fatalf("err %v", err)
			}
			if len(ss) != tc.want {
				t.Fatalf("sessions = %d, want %d (%#v)", len(ss), tc.want, ss)
			}
			if tc.want == 0 {
				return
			}
			if ss[0].ID != tc.id {
				t.Fatalf("id = %q, want %q", ss[0].ID, tc.id)
			}
			if ss[0].Harness != "copilot-chat" {
				t.Fatalf("harness = %q", ss[0].Harness)
			}
			if ss[0].Messages[0].Text != tc.user {
				t.Fatalf("user = %q, want %q", ss[0].Messages[0].Text, tc.user)
			}
			if got := ss[0].Messages[0].Time.UnixMilli(); got != ts {
				t.Fatalf("first user time = %d, want %d", got, ts)
			}
			if ss[0].Started.IsZero() || ss[0].Updated.IsZero() {
				t.Fatalf("times zero: %v %v", ss[0].Started, ss[0].Updated)
			}
		})
	}

	t.Run("push-v-two-items-count", func(t *testing.T) {
		p := filepath.Join(root, "push-v-two-items.jsonl")
		ss, err := ParseCopilotChatFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%v %#v", err, ss)
		}
		var users int
		for _, m := range ss[0].Messages {
			if m.Role == "user" {
				users++
			}
		}
		if users != 2 {
			t.Fatalf("users = %d, want 2", users)
		}
	})
	t.Run("truncate-keeps-two", func(t *testing.T) {
		p := filepath.Join(root, "truncate-push.jsonl")
		ss, _ := ParseCopilotChatFile(p)
		var users []string
		for _, m := range ss[0].Messages {
			if m.Role == "user" {
				users = append(users, m.Text)
			}
		}
		if strings.Join(users, ",") != "keep,new" {
			t.Fatalf("users = %v", users)
		}
	})
	t.Run("kind1-set-title-applied", func(t *testing.T) {
		p := filepath.Join(root, "kind1-set-title.jsonl")
		ss, _ := ParseCopilotChatFile(p)
		if ss[0].Title != "from set" {
			t.Fatalf("title = %q", ss[0].Title)
		}
	})
}

func TestParseCopilotChatFlatJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{"version":3,"sessionId":"flat","creationDate":1763727100000,"customTitle":"flat title","requests":[` +
		copilotChatReq("r1", "ask", "ans", 1763727104742, 1763727400000) + `]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse = %#v, %v", ss, err)
	}
	if ss[0].ID != "flat" || ss[0].Title != "flat title" {
		t.Fatalf("meta %#v", ss[0])
	}
	if len(ss[0].Messages) != 2 || ss[0].Messages[1].Text != "ans" {
		t.Fatalf("messages %#v", ss[0].Messages)
	}
}

func TestParseCopilotChatStringResponseAndV2Title(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "str.json")
	body := `{"version":3,"sessionId":"s","creationDate":1763727100000,"requests":[{` +
		`"timestamp":1763727104742,"message":"q","response":"plain reply","responseTimestamp":1763727400000}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 || ss[0].Messages[1].Text != "plain reply" {
		t.Fatalf("string response %#v, %v", ss, err)
	}

	p2 := filepath.Join(dir, "v2.json")
	v2 := `{"version":2,"computedTitle":"old title` + "\\n" + `second line","sessionId":"v2","creationDate":1763727100000,"customTitle":"ignored","requests":[{` +
		`"timestamp":1763727104742,"message":"q","response":[{"value":"a"}],"responseTimestamp":1763727400000}]}`
	if err = os.WriteFile(p2, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err = ParseCopilotChatFile(p2)
	if err != nil || len(ss) != 1 || ss[0].Title != "old title" {
		t.Fatalf("v2 title = %#v, %v", ss, err)
	}

	p3 := filepath.Join(dir, "nover.json")
	nover := `{"customTitle":"should-not-stick","sessionId":"n","creationDate":1763727100000,"requests":[{` +
		`"timestamp":1763727104742,"message":"q","response":[{"value":"a"}],"responseTimestamp":1763727400000}]}`
	if err = os.WriteFile(p3, []byte(nover), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err = ParseCopilotChatFile(p3)
	if err != nil || len(ss) != 1 || ss[0].Title != "" {
		t.Fatalf("missing version title = %q, %v", ss[0].Title, err)
	}
}

func TestParseCopilotChatMissingFile(t *testing.T) {
	if _, err := ParseCopilotChatFile(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("expected read error")
	}
}

func TestCopilotChatAccountLabelNeverIndexed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"kind":0,"v":{"version":3,"sessionId":"s","creationDate":1763727100000,` +
		`"inputState":{"selectedModel":{"metadata":{"auth":{"accountLabel":"someone"}}}},` +
		`"requests":[` + copilotChatReq("r", "the retry loop", "ok", 1763727104742, 1763727400000) + `]}}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "someone") {
			t.Fatalf("accountLabel leaked into %s: %q", m.Role, m.Text)
		}
	}
}

func TestCopilotChatSkipsThinkingAndIndexesTools(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{
		"version":3,"sessionId":"s","creationDate":1763727100000,
		"requests":[{
			"timestamp":1763727104742,
			"message":{"text":"fix retry"},
			"response":[
				{"kind":"thinking","value":"secret-reason"},
				{"kind":"progressMessage","content":{"value":"working"}},
				{"value":"visible answer, see "},
				{"kind":"toolInvocationSerialized","toolId":"copilot_readFile",
					"resultDetails":[{"path":"/w/retry.go"}],
					"toolSpecificData":{"kind":"terminal","commandLine":{"original":"go test ./...","userEdited":"go test"}}},
				{"kind":"inlineReference","inlineReference":{"path":"/w/other.go"}}
			],
			"responseTimestamp":1763727400000
		}]
	}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	var assistant, files, cmds []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case "assistant":
			assistant = append(assistant, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		case RoleCommand:
			cmds = append(cmds, m.Text)
		}
	}
	// The inline reference is part of the sentence VS Code shows (#4589).
	if len(assistant) != 1 || assistant[0] != "visible answer, see other.go" {
		t.Fatalf("assistant = %v", assistant)
	}
	joined := strings.Join(assistant, "\n")
	if strings.Contains(joined, "secret-reason") || strings.Contains(joined, "working") {
		t.Fatalf("chrome leaked: %q", joined)
	}
	if strings.Join(files, ",") != "/w/retry.go,/w/other.go" {
		t.Fatalf("files = %v", files)
	}
	if strings.Join(cmds, ",") != "$ go test" {
		t.Fatalf("cmds = %v", cmds)
	}
}

func TestCopilotChatProjectFromWorkspaceJSON(t *testing.T) {
	root := copilotChatRoot(t)
	posix := writeCopilotChatSession(t, root, "hash1", "s1",
		`{"folder":"file:///tmp/registry-demo"}`,
		`{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[`+
			copilotChatReq("r", "q", "a", 1763727104742, 1763727400000)+`]}}`+"\n")
	ss, err := ParseCopilotChatFile(posix)
	if err != nil || len(ss) != 1 || ss[0].Project != "tmp/registry-demo" {
		t.Fatalf("posix project = %#v, %v", ss, err)
	}

	win := writeCopilotChatSession(t, root, "hash2", "s2",
		`{"folder":"file:///c%3A/Users/x/proj/demo"}`,
		`{"kind":0,"v":{"version":3,"sessionId":"s2","creationDate":1763727100000,"requests":[`+
			copilotChatReq("r", "q", "a", 1763727104742, 1763727400000)+`]}}`+"\n")
	ss, err = ParseCopilotChatFile(win)
	if err != nil || len(ss) != 1 || ss[0].Project != "proj/demo" {
		t.Fatalf("windows c%%3A project = %#v, %v", ss, err)
	}

	multi := writeCopilotChatSession(t, root, "hash3", "s3",
		`{"workspace":"file:///tmp/multi.code-workspace"}`,
		`{"kind":0,"v":{"version":3,"sessionId":"s3","creationDate":1763727100000,"requests":[`+
			copilotChatReq("r", "q", "a", 1763727104742, 1763727400000)+`]}}`+"\n")
	ss, err = ParseCopilotChatFile(multi)
	if err != nil || len(ss) != 1 || ss[0].Project != "tmp/multi.code-workspace" {
		t.Fatalf("workspace uri project = %#v, %v", ss, err)
	}
}

func TestCopilotChatEmptyWindowWorkingDirectory(t *testing.T) {
	root := copilotChatRoot(t)
	dir := filepath.Join(root, "globalStorage", "emptyWindowChatSessions")
	p := writeCopilotChatJSONL(t, filepath.Join(dir, "ew.jsonl"), []string{
		`{"kind":0,"v":{"version":3,"sessionId":"ew","creationDate":1763727100000,` +
			`"workingDirectory":"file:///tmp/other-proj","requests":[` +
			copilotChatReq("r", "q", "a", 1763727104742, 1763727400000) + `]}}`,
	})
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 || ss[0].Project != "tmp/other-proj" {
		t.Fatalf("empty-window project = %#v, %v", ss, err)
	}
}

func TestCopilotChatSessionFilesDedupeAndDiscovery(t *testing.T) {
	root := copilotChatRoot(t)
	body := `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[` +
		copilotChatReq("r", "q", "a", 1763727104742, 1763727400000) + `]}}` + "\n"
	jsonl := writeCopilotChatSession(t, root, "h", "s1", `{"folder":"file:///tmp/p"}`, body)
	flat := strings.TrimSuffix(jsonl, ".jsonl") + ".json"
	if err := os.WriteFile(flat, []byte(`{"version":3,"sessionId":"s1","requests":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	xfer := filepath.Join(root, "globalStorage", "transferredChatSessions")
	if err := os.MkdirAll(xfer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xfer, "x.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ew := filepath.Join(root, "globalStorage", "emptyWindowChatSessions")
	if err := os.MkdirAll(ew, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ew, "ew.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	files := CopilotChatSessionFiles()
	bases := make([]string, 0, len(files))
	for _, f := range files {
		bases = append(bases, filepath.Base(f))
		if strings.Contains(f, "transferredChatSessions") {
			t.Fatalf("transferred listed: %s", f)
		}
		if strings.HasSuffix(f, ".json") && !strings.HasSuffix(f, ".jsonl") {
			t.Fatalf("json sibling listed: %s", f)
		}
	}
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	got := strings.Join(bases, ",")
	if !strings.Contains(got, "s1.jsonl") || !strings.Contains(got, "ew.jsonl") {
		t.Fatalf("bases = %v", bases)
	}
	if KindForPath(jsonl) != "copilot-chat" {
		t.Fatalf("KindForPath = %q", KindForPath(jsonl))
	}
	if KindForPath(filepath.Join(xfer, "x.jsonl")) != "" {
		t.Fatal("transferred should not match")
	}
	ss := LoadCopilotChat()
	if len(ss) != 2 {
		t.Fatalf("LoadCopilotChat = %d", len(ss))
	}
}

func TestCopilotChatRootsEnvOverride(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", missing)
	got := CopilotChatRoots()
	if len(got) != 1 || got[0] != missing {
		t.Fatalf("roots = %v", got)
	}
}

func TestParseCopilotChatFilenameStemFallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "stem-id.json")
	body := `{"version":3,"creationDate":1763727100000,"requests":[` +
		copilotChatReq("r", "q", "a", 1763727104742, 1763727400000) + `]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 || ss[0].ID != "stem-id" {
		t.Fatalf("id fallback %#v, %v", ss, err)
	}
}

func TestParseCopilotChatEmptyRequestsDropped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(p, []byte(`{"version":3,"sessionId":"e","requests":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 0 {
		t.Fatalf("empty session %#v, %v", ss, err)
	}
}

// A push whose index is past the end pads with holes first, the way
// `arr.length = i` does in VS Code's reader, so the pushed request lands at
// the index the writer meant and not two slots early.
func TestCopilotChatPushPastTheEndPadsLikeVSCode(t *testing.T) {
	body := `{"kind":0,"v":{"version":3,"sessionId":"s1","requests":[]}}` + "\n" +
		`{"kind":2,"k":["requests"],"v":[{"requestId":"r"}],"i":2}` + "\n"
	state, ok := copilotChatReplay("pad.jsonl", []byte(body))
	if !ok {
		t.Fatal("replay failed")
	}
	reqs, _ := state["requests"].([]any)
	if len(reqs) != 3 || reqs[0] != nil || reqs[1] != nil {
		t.Fatalf("requests after padded push = %#v, want two holes then the request", reqs)
	}
	if m, _ := reqs[2].(map[string]any); m["requestId"] != "r" {
		t.Fatalf("pushed request not at index 2: %#v", reqs[2])
	}
}

// VS Code appends to a chat's .jsonl while the reply streams, so a read can
// land on a last line that is only half there. The complete lines are the
// session; the torn tail waits for the next pass (#4229). A bad line with more
// after it is still a broken file.
func TestCopilotChatTornLastLineKeepsTheSession(t *testing.T) {
	const ts, rts = int64(1763727100000), int64(1763727160000)
	snap := `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[]}}`
	push := `{"kind":2,"k":["requests"],"v":[` + copilotChatReq("a", "first question", "A", ts, rts) + `]}`
	torn := `{"kind":2,"k":["requests"],"v":[{"requestId":"b","message":{"text":"half writ`
	dir := t.TempDir()

	p := filepath.Join(dir, "torn.jsonl")
	if err := os.WriteFile(p, []byte(snap+"\n"+push+"\n"+torn), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("a torn last line dropped the session: %v %d", err, len(ss))
	}
	if ss[0].Messages[0].Text != "first question" {
		t.Fatalf("user = %q", ss[0].Messages[0].Text)
	}

	mid := filepath.Join(dir, "mid.jsonl")
	if err := os.WriteFile(mid, []byte(snap+"\n"+torn+"\n"+push+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ss, _ := ParseCopilotChatFile(mid); len(ss) != 0 {
		t.Fatalf("a broken line in the middle was skipped: %d sessions", len(ss))
	}
}

// Copilot Chat keeps an edit as a range plus the text that replaced it, so the
// written side is there in full and the replaced side is not there at all. The
// store held 655 of these and the index read none (#595).
func TestCopilotChatRecordsTheWrittenSideOfAnEdit(t *testing.T) {
	const written = "func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {"
	const second = "\treturn p.acquireWithTimeout(ctx, p.cfg.AcquireTimeout)"
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{
		"version":3,"sessionId":"s","creationDate":1763727100000,
		"requests":[{
			"timestamp":1763727104742,
			"message":{"text":"give the pool an acquire timeout"},
			"response":[
				{"kind":"textEditGroup",
					"uri":{"$mid":1,"fsPath":"/w/app/internal/pool/pool.go","path":"/w/app/internal/pool/pool.go","scheme":"file"},
					"edits":[[{"text":"` + written + `\n` + strings.ReplaceAll(second, "\t", "\\t") + `","range":{"startLineNumber":1,"startColumn":1,"endLineNumber":3,"endColumn":2}}],[]],
					"done":true}
			],
			"responseTimestamp":1763727400000
		}]
	}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	var files, wrote []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleFiles:
			files = append(files, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	if strings.Join(files, ",") != "/w/app/internal/pool/pool.go" {
		t.Fatalf("files = %v", files)
	}
	if len(wrote) != 1 {
		t.Fatalf("wrote = %q, want one record", wrote)
	}
	// The record's hash set is exactly the two written lines: the path line is
	// not a written line, and a record holding anything else would attribute a
	// line this session never wrote.
	want := make([]string, 0, 2)
	for _, line := range []string{written, second} {
		h, ok := HashWrittenLine(line)
		if !ok {
			t.Fatalf("%q is too short to be evidence", line)
		}
		want = append(want, strconv.FormatUint(h, 16))
	}
	path, rest, ok := strings.Cut(wrote[0], "\n")
	if !ok || path != "/w/app/internal/pool/pool.go" {
		t.Fatalf("the record is filed under %q", path)
	}
	got := strings.Fields(rest)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("hashes = %v, want the two written lines %v", got, want)
	}
	// Nothing in the store says what was replaced, so nothing may claim to.
	for _, m := range ss[0].Messages {
		if m.Role == RoleEdit {
			t.Fatalf("a replaced span was invented: %q", m.Text)
		}
	}
}

// The switches are the contract: a store that indexes neither writes nor tool
// paths gets neither from this reader.
func TestCopilotChatEditsRespectTheIndexSwitches(t *testing.T) {
	t.Setenv("DEJA_INDEX_WRITES", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{
		"version":3,"sessionId":"s","creationDate":1763727100000,
		"requests":[{
			"timestamp":1763727104742,
			"message":{"text":"give the pool an acquire timeout"},
			"response":[
				{"kind":"textEditGroup",
					"uri":{"path":"/w/app/internal/pool/pool.go"},
					"edits":[[{"text":"func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {"}]],
					"done":true}
			],
			"responseTimestamp":1763727400000
		}]
	}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleWrote || m.Role == RoleFiles {
			t.Fatalf("%s survived its switch: %q", m.Role, m.Text)
		}
	}
}

// Copilot records every completion with `"success": true`, failed runs
// included, and puts the outcome in the telemetry and in a trailer under the
// output. deja took the field at its word, so no command carried an outcome
// and `deja fix` answered nothing for the harness (#3369).
func TestCopilotMarksACommandThatExitedNonZero(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "7dad4ddc")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess, "events.jsonl")
	lines := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-09-05T10:00:00Z","data":{"sessionId":"7dad4ddc","startTime":"2026-09-05T10:00:00Z","context":{"cwd":"/w/api"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"call-1","arguments":{"command":"go vet ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"call-1","success":true,"result":{"content":"pattern ./...: directory prefix . does not contain main module\n<shellId: 0 completed with exit code 1>"},"toolTelemetry":{"metrics":{"commandTimeout":30000,"exit_code":1}}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:03Z","data":{"toolName":"bash","toolCallId":"call-2","arguments":{"command":"go mod init api && go vet ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:04Z","data":{"toolCallId":"call-2","success":true,"result":{"content":"ok\n<shellId: 0 completed with exit code 0>"},"toolTelemetry":{"metrics":{"commandTimeout":30000}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCopilotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("commands = %d, want 2: %q", len(cmds), cmds)
	}
	if !strings.HasSuffix(cmds[0], "  → exit 1") {
		t.Errorf("the failed run reads %q, want it to end in the exit marker", cmds[0])
	}
	if !strings.HasSuffix(cmds[1], "  → exit 0") {
		t.Errorf("the run that succeeded reads %q, want it to end in exit 0", cmds[1])
	}
}

// And the outcome is what the fix pair reads: with it, the command that
// followed a failed run is the remedy; without it the failed run itself could
// be handed back as one (#3369).
func TestCopilotFailedRunIsNotOfferedAsTheRemedy(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "s1")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess, "events.jsonl")
	lines := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-09-05T10:00:00Z","data":{"sessionId":"s1","startTime":"2026-09-05T10:00:00Z","context":{"cwd":"/w/api"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"c1","arguments":{"command":"go build ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"c1","success":true,"result":{"content":"queue/retry.go:12:2: undefined: backoffCap\n<shellId: 0 completed with exit code 1>"},"toolTelemetry":{"metrics":{"exit_code":1}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand && !strings.Contains(m.Text, "→ exit 1") {
			t.Errorf("the only command in the session failed and reads %q", m.Text)
		}
	}
}

// The scanner decodes numbers with UseNumber, so the telemetry's exit code
// arrives as json.Number. Asserting float64 made that path dead and left every
// marker to the trailer, which a run without one never has (review of #3369).
func TestCopilotReadsTheExitCodeFromTelemetryWithoutATrailer(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "s2")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess, "events.jsonl")
	lines := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-09-05T10:00:00Z","data":{"sessionId":"s2","startTime":"2026-09-05T10:00:00Z","context":{"cwd":"/w/api"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"c1","arguments":{"command":"go test ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"c1","success":true,"result":{"content":"--- FAIL: TestRetry (0.01s)\nFAIL\tqueue\t0.4s"},"toolTelemetry":{"metrics":{"exit_code":1}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand && !strings.HasSuffix(m.Text, "  → exit 1") {
			t.Errorf("command = %q, want the outcome the telemetry recorded", m.Text)
		}
	}
}

// One outcome belongs to one call: an id reused later must not put its exit
// code on a run that already finished (second review of #3369).
func TestCopilotDoesNotMarkAnEarlierRunWithALaterOutcome(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "s3")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess, "events.jsonl")
	lines := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-09-05T10:00:00Z","data":{"sessionId":"s3","startTime":"2026-09-05T10:00:00Z","context":{"cwd":"/w/api"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"dup","arguments":{"command":"go build ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"dup","success":true,"result":{"content":"ok"},"toolTelemetry":{"metrics":{"exit_code":0}}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:03Z","data":{"toolName":"bash","toolCallId":"dup","arguments":{"command":"go test ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:04Z","data":{"toolCallId":"dup","success":true,"result":{"content":"--- FAIL: TestRetry"},"toolTelemetry":{"metrics":{"exit_code":1}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("commands = %d, want 2: %q", len(cmds), cmds)
	}
	if !strings.HasSuffix(cmds[0], "  → exit 0") {
		t.Errorf("the run that succeeded reads %q, want it to end in exit 0", cmds[0])
	}
	if !strings.HasSuffix(cmds[1], "  → exit 1") {
		t.Errorf("the run that failed lost its outcome: %q", cmds[1])
	}
}

// Copilot CLI injects a skill's body as a user.message wrapped in
// <skill-context>; the reader indexed the whole skill as the person's words
// (#3305). Shape from a real 1.0.79 store.
func TestCopilotSkillContextIsNotThePersons(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	dir := filepath.Join(root, "a553c093-4f69-48ce-8e82-669ece70637d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"type":"session.start","data":{"sessionId":"a553c093-4f69-48ce-8e82-669ece70637d"},"timestamp":"2026-09-08T10:00:00.000Z"}`,
		`{"type":"user.message","data":{"content":"<skill-context name=\"deja-history\">\nBase directory for this skill: /Users/me/.copilot/skills/deja-history\n\ndeja does not index Copilot history. It is a consumer: use the deja MCP tools to search memory from the other harnesses.\n</skill-context>"},"timestamp":"2026-09-08T10:00:01.000Z"}`,
		`{"type":"user.message","data":{"content":"<skill-context name=\"deja-history\">\nBase directory for this skill: /Users/me/.copilot/skills/deja-history\n</skill-context>\n\nwhat did we settle about the checkout worker"},"timestamp":"2026-09-08T10:00:02.000Z"}`,
		`{"type":"assistant.message","data":{"content":"It keeps one connection per worker."},"timestamp":"2026-09-08T10:00:05.000Z"}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(filepath.Join(dir, "events.jsonl"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 1 || users[0] != "what did we settle about the checkout worker" {
		t.Errorf("user turns = %q, want the typed question alone", users)
	}
}

const (
	copilotParentID = "f67048ad-042c-42f4-ac33-5db51c4d48b0"
	copilotChildID  = "717532a2-bf69-4164-be09-03daacd1762f"
	copilotForkID   = "2f07ff7f-aa0e-4b85-93fb-2096b6e9833f"
)

// The shape Copilot CLI 1.0.98 writes for a `task` call: the sub-agent's
// records sit in the parent's events.jsonl, each one stamped with agentId.
func copilotSubagentLines(sessionID, start string) []string {
	return []string{
		`{"type":"session.start","data":{"sessionId":"` + sessionID + `","copilotVersion":"1.0.98","startTime":"` + start + `","context":{"cwd":"/Users/x/work/my-app"}},"timestamp":"` + start + `","parentId":null}`,
		`{"type":"user.message","data":{"content":"write the release notes with a helper agent"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"call_task","toolName":"task","arguments":{"description":"write notes","prompt":"draft the release notes file","agent_type":"general-purpose","name":"scribe"}},"timestamp":"2026-10-18T05:31:47.355Z"}`,
		`{"type":"subagent.started","agentId":"` + copilotChildID + `","data":{"toolCallId":"call_task","agentName":"general-purpose","agentDisplayName":"scribe","agentType":"general-purpose","resumable":false},"timestamp":"2026-10-18T05:31:47.356Z"}`,
		`{"type":"user.message","agentId":"` + copilotChildID + `","data":{"content":"draft the release notes file"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
		`{"type":"tool.execution_start","agentId":"` + copilotChildID + `","data":{"toolCallId":"call_create","toolName":"create","arguments":{"path":"/Users/x/work/my-app/notes.txt","file_text":"release notes drafted by the helper sub-agent\n"},"parentToolCallId":"call_task"},"timestamp":"2026-10-18T05:31:47.407Z"}`,
		`{"type":"tool.execution_complete","agentId":"` + copilotChildID + `","data":{"toolCallId":"call_create","success":true,"result":{"content":"Created file notes.txt"}},"timestamp":"2026-10-18T05:31:47.426Z"}`,
		`{"type":"assistant.message","agentId":"` + copilotChildID + `","data":{"content":"Created notes.txt with the draft.","parentToolCallId":"call_task"},"timestamp":"2026-10-18T05:31:49.063Z"}`,
		`{"type":"subagent.completed","agentId":"` + copilotChildID + `","data":{"toolCallId":"call_task","agentName":"general-purpose"},"timestamp":"2026-10-18T05:31:49.064Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"call_task","success":true,"result":{"content":"Created notes.txt with the draft."}},"timestamp":"2026-10-18T05:31:49.067Z"}`,
		`{"type":"assistant.message","data":{"content":"The helper wrote notes.txt."},"timestamp":"2026-10-18T05:31:51.660Z"}`,
		`{"type":"user.message","data":{"content":"the parent writes its own file"},"timestamp":"2026-10-18T05:33:10.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"call_parent","toolName":"create","arguments":{"path":"/Users/x/work/my-app/parent.txt","file_text":"the parent session wrote this whole line itself\n"}},"timestamp":"2026-10-18T05:33:10.100Z"}`,
	}
}

func copilotByID(ss []model.Session) map[string]model.Session {
	out := map[string]model.Session{}
	for _, s := range ss {
		out[s.ID] = s
	}
	return out
}

func copilotHasText(s model.Session, role, sub string) bool {
	for _, m := range s.Messages {
		if m.Role == role && strings.Contains(m.Text, sub) {
			return true
		}
	}
	return false
}

// A sub-agent's records were folded into the parent: the prompt the parent
// agent wrote for it read as the person's turn, and its edits as the parent's
// (#5216).
func TestCopilotSubagentIsItsOwnSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	p := writeCopilotFixture(t, root, copilotParentID, copilotSubagentLines(copilotParentID, "2026-10-18T05:31:46.707Z"))
	ss, err := ParseCopilotFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 || ss[0].ID != copilotParentID {
		t.Fatalf("sessions = %d (first %q), want the parent then its sub-agent", len(ss), ss[0].ID)
	}
	by := copilotByID(ss)
	parent, child := by[copilotParentID], by[copilotChildID]
	if parent.Kind != "" || parent.Parent != "" {
		t.Errorf("parent kind/parent = %q/%q", parent.Kind, parent.Parent)
	}
	if copilotHasText(parent, "user", "draft the release notes file") {
		t.Errorf("the sub-agent's task prompt reads as the person's turn in the parent")
	}
	if copilotHasText(parent, RoleWrote, "notes.txt") {
		t.Errorf("the sub-agent's write is filed under the parent")
	}
	if !copilotHasText(parent, RoleWrote, "parent.txt") || !copilotHasText(parent, "user", "write the release notes") {
		t.Errorf("parent lost its own records: %#v", parent.Messages)
	}
	if child.Kind != "sidechain" || child.Parent != copilotParentID || child.Agent != "general-purpose" {
		t.Errorf("child kind/parent/agent = %q/%q/%q", child.Kind, child.Parent, child.Agent)
	}
	if child.Harness != "copilot" || child.Project != parent.Project || child.Path != p {
		t.Errorf("child harness/project/path = %q/%q/%q", child.Harness, child.Project, child.Path)
	}
	if !copilotHasText(child, "user", "draft the release notes file") || !copilotHasText(child, RoleWrote, "notes.txt") ||
		!copilotHasText(child, "assistant", "Created notes.txt") {
		t.Errorf("child records = %#v", child.Messages)
	}

	// A tail that closes a run goes back for a whole read, which cuts it the
	// way a rebuild does; one inside the run, or past it, is read on its own.
	lines := copilotSubagentLines(copilotParentID, "2026-10-18T05:31:46.707Z")
	if off := int64(len(strings.Join(lines[:5], "\n") + "\n")); copilotResumes(p, off) {
		t.Errorf("a tail holding subagent.completed is read on its own")
	}
	if off := int64(len(strings.Join(lines[:6], "\n") + "\n")); copilotResumes(p, off) {
		t.Errorf("a tail holding subagent.completed is read on its own")
	}
	if off := int64(len(strings.Join(lines[:9], "\n") + "\n")); !copilotResumes(p, off) {
		t.Errorf("a tail past the run is read whole")
	}
	mid := writeCopilotFixture(t, root, "mid", lines[:7])
	if off := int64(len(strings.Join(lines[:5], "\n") + "\n")); !copilotResumes(mid, off) {
		t.Errorf("a tail in the middle of a run is read whole")
	}
	// A run cut off with no subagent.completed heals when the session ends.
	ended := writeCopilotFixture(t, root, "ended", append(append([]string{}, lines[:7]...), `{"type":"session.shutdown","data":{},"timestamp":"2026-10-18T05:40:00.000Z"}`))
	if off := int64(len(strings.Join(lines[:5], "\n") + "\n")); copilotResumes(ended, off) {
		t.Errorf("a tail holding session.shutdown is read on its own")
	}
	if ss, _ := ParseCopilotFileFromOffset(mid, int64(len(strings.Join(lines[:5], "\n")+"\n"))); copilotByID(ss)[copilotChildID].Parent != copilotParentID {
		t.Errorf("a tail in the middle of a run lost its parent: %#v", ss)
	}

	// DEJA_INCLUDE_SUBAGENTS=0 leaves the run out, and still not in the parent.
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	off, _ := ParseCopilotFile(p)
	if len(off) != 1 || copilotHasText(off[0], "user", "draft the release notes file") {
		t.Errorf("with =0: %d sessions, parent %#v", len(off), off[0].Messages)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")

	// The hooks read the head of the file as the parent's, child records in it
	// or not.
	head, err := CopilotSessionHead(copilotParentID)
	if err != nil || head.ID != copilotParentID || head.Kind != "" {
		t.Errorf("head = %q %q, %v", head.ID, head.Kind, err)
	}
}

// /fork copies the source's records with their times, the sub-agent's among
// them, then says where it came from in a session.info record. The fork named
// no source, and its copy of the sub-agent claimed the same id as the original
// (#5217).
func TestCopilotForkNamesItsSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotSubagentLines(copilotForkID, "2026-10-18T05:34:00.000Z")
	lines = append(lines,
		`{"type":"session.info","data":{"infoType":"fork","message":"Forked from \"write the release notes (f00dfeed-0000-4000-8000-000000000000) here\" (`+copilotParentID+`) as \"write the release notes (fork)\" (`+copilotForkID+`)."},"timestamp":"2026-10-18T05:34:00.002Z"}`,
		`{"type":"user.message","data":{"content":"hi from the fork"},"timestamp":"2026-10-18T05:34:10.000Z"}`,
		`{"type":"subagent.started","agentId":"9e0b1c2d-0000-4000-8000-00000000f0f0","data":{"toolCallId":"call_t2","agentName":"explore"},"timestamp":"2026-10-18T05:34:11.000Z"}`,
		`{"type":"assistant.message","agentId":"9e0b1c2d-0000-4000-8000-00000000f0f0","data":{"content":"the fork's own helper answered"},"timestamp":"2026-10-18T05:34:12.000Z"}`,
	)
	p := writeCopilotFixture(t, root, copilotForkID, lines)
	ss, err := ParseCopilotFile(p)
	if err != nil {
		t.Fatal(err)
	}
	by := copilotByID(ss)
	fork := by[copilotForkID]
	if fork.Kind != "fork" || fork.Parent != copilotParentID {
		t.Errorf("fork kind/parent = %q/%q, want fork/%s", fork.Kind, fork.Parent, copilotParentID)
	}
	if _, ok := by[copilotChildID]; ok {
		t.Errorf("the fork's copy of the source's sub-agent is read as a session under the original's id")
	}
	if own := by["9e0b1c2d-0000-4000-8000-00000000f0f0"]; own.Parent != copilotForkID || own.Kind != "sidechain" || own.Agent != "explore" {
		t.Errorf("the fork's own sub-agent = %q/%q/%q", own.Kind, own.Parent, own.Agent)
	}
	if !copilotHasText(fork, "user", "hi from the fork") || !copilotHasText(fork, RoleWrote, "parent.txt") {
		t.Errorf("fork records = %#v", fork.Messages)
	}
	if len(ss) != 2 {
		t.Errorf("sessions = %d, want the fork and its own sub-agent", len(ss))
	}

	// Read before its note is written, the fork's copy of the run is still
	// not a session: its records predate the fork's start.
	early := writeCopilotFixture(t, root, "early", copilotSubagentLines(copilotForkID, "2026-10-18T05:34:00.000Z"))
	es, _ := ParseCopilotFile(early)
	if _, ok := copilotByID(es)[copilotChildID]; ok || len(es) != 1 {
		t.Errorf("a fork read before its note: %d sessions, copied run kept = %v", len(es), ok)
	}

	// Without startTime the record's own time is the fork's start.
	forkLines := copilotSubagentLines(copilotForkID, "2026-10-18T05:34:00.000Z")
	bare := make([]string, 0, len(forkLines))
	for _, l := range forkLines {
		bare = append(bare, strings.Replace(l, `"startTime":"2026-10-18T05:34:00.000Z",`, "", 1))
	}
	bs, _ := ParseCopilotFile(writeCopilotFixture(t, root, "bare", bare))
	if _, ok := copilotByID(bs)[copilotChildID]; ok {
		t.Errorf("with no startTime, a fork read before its note keeps the copied run")
	}

	// The source's own note about the fork names the fork, not a source.
	src := copilotSubagentLines(copilotParentID, "2026-10-18T05:31:46.707Z")
	src = append(src, `{"type":"session.info","data":{"infoType":"fork","message":"Forked this session into \"write the release notes (fork)\" (`+copilotForkID+`)."},"timestamp":"2026-10-18T05:34:00.004Z"}`)
	ps, _ := ParseCopilotFile(writeCopilotFixture(t, root, copilotParentID, src))
	if s := copilotByID(ps)[copilotParentID]; s.Kind != "" || s.Parent != "" {
		t.Errorf("source read as a fork: %q/%q", s.Kind, s.Parent)
	}
}

// By default a long run is cut the way a Claude Code sub-agent file is: the
// task, the last turns and what it changed (#5216).
func TestCopilotSubagentKeepsItsTaskTailAndEdits(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotSubagentLines(copilotParentID, "2026-10-18T05:31:46.707Z")
	run := lines[:8]
	for i := range 6 {
		run = append(run, `{"type":"assistant.message","agentId":"`+copilotChildID+`","data":{"content":"reading more of the repo, step `+string(rune('a'+i))+`"},"timestamp":"2026-10-18T05:31:50.00`+string(rune('0'+i))+`Z"}`)
	}
	p := writeCopilotFixture(t, root, copilotParentID, append(run, lines[8:]...))
	for _, tc := range []struct {
		include string
		whole   bool
	}{{"", false}, {"1", true}} {
		t.Setenv("DEJA_INCLUDE_SUBAGENTS", tc.include)
		ss, _ := ParseCopilotFile(p)
		c := copilotByID(ss)[copilotChildID]
		if !copilotHasText(c, "user", "draft the release notes file") || !copilotHasText(c, RoleWrote, "notes.txt") {
			t.Errorf("include=%q: task or write lost: %#v", tc.include, c.Messages)
		}
		if got := copilotHasText(c, "assistant", "step a"); got != tc.whole {
			t.Errorf("include=%q: middle turn kept = %v, want %v", tc.include, got, tc.whole)
		}
	}
}

func writeCopilotFixture(t *testing.T, root, id string, lines []string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func copilotFixtureLines() []string {
	return []string{
		`{"type":"session.start","data":{"sessionId":"7cf44517-55ca-435c-893a-3fde1973a44e","startTime":"2026-07-20T05:13:54.687Z","context":{"cwd":"/Users/x/coding/gateway"}},"timestamp":"2026-07-20T05:13:54.707Z"}`,
		`{"type":"session.model_change","data":{"newModel":"gpt-5-mini"},"timestamp":"2026-07-20T05:13:58.117Z"}`,
		`{"type":"system.message","data":{"role":"system","content":"You are the GitHub Copilot CLI"},"timestamp":"2026-07-20T05:13:58.500Z"}`,
		`{"type":"user.message","data":{"content":"why is the pool exhausted"},"timestamp":"2026-07-20T05:13:58.653Z"}`,
		`{"type":"assistant.turn_start","data":{},"timestamp":"2026-07-20T05:13:58.678Z"}`,
		`{"type":"assistant.message","data":{"content":"the metrics poller leaks rows"},"timestamp":"2026-07-20T05:14:00.973Z"}`,
		`{"type":"session.shutdown","data":{},"timestamp":"2026-07-20T05:14:01.029Z"}`,
	}
}

func TestParseCopilotFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	p := writeCopilotFixture(t, root, "dir-name-not-id", copilotFixtureLines())
	ss, err := ParseCopilotFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse = %v, %v", ss, err)
	}
	s := ss[0]
	if s.Harness != "copilot" || s.ID != "7cf44517-55ca-435c-893a-3fde1973a44e" {
		t.Fatalf("session meta = %#v", s)
	}
	if s.Project != "coding/gateway" {
		t.Fatalf("project = %q", s.Project)
	}
	if len(s.Messages) != 2 || s.Messages[0].Role != "user" || s.Messages[1].Text != "the metrics poller leaks rows" {
		t.Fatalf("messages = %#v", s.Messages)
	}
	if s.Started.IsZero() || s.Updated.Before(s.Started) {
		t.Fatalf("times = %v %v", s.Started, s.Updated)
	}
}

func TestParseCopilotFileFromOffset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotFixtureLines()
	p := writeCopilotFixture(t, root, "abc", lines)
	head := strings.Join(lines[:6], "\n") + "\n"
	ss, err := ParseCopilotFileFromOffset(p, int64(len(head)))
	if err != nil {
		t.Fatal(err)
	}
	// Only session.shutdown remains past the offset: no messages, so no session.
	if len(ss) != 0 {
		t.Fatalf("offset tail sessions = %#v", ss)
	}
	off := int64(len(strings.Join(lines[:3], "\n") + "\n"))
	ss, err = ParseCopilotFileFromOffset(p, off)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("offset parse = %#v, %v", ss, err)
	}
	// session.start is behind the offset; its id still names the session.
	if ss[0].ID != "7cf44517-55ca-435c-893a-3fde1973a44e" {
		t.Fatalf("id = %q", ss[0].ID)
	}
}

// An index run between session.start and the first prompt stores the file's
// offset with no session; the rest of the file then arrives as a tail. It has
// to carry what a rebuild reads off session.start, or the session is filed
// under no project and drops out of that project's start digest and prompt
// recall (#5136).
func TestCopilotTailReadKeepsSessionStart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotFixtureLines()
	p := writeCopilotFixture(t, root, "7cf44517-55ca-435c-893a-3fde1973a44e", lines)
	full, err := ParseCopilotFile(p)
	if err != nil || len(full) != 1 {
		t.Fatalf("full parse = %#v, %v", full, err)
	}
	ss, err := ParseCopilotFileFromOffset(p, int64(len(lines[0])+1))
	if err != nil || len(ss) != 1 {
		t.Fatalf("tail parse = %#v, %v", ss, err)
	}
	got, want := ss[0], full[0]
	if got.Project != want.Project || got.ID != want.ID || !got.Started.Equal(want.Started) {
		t.Fatalf("tail = id %q project %q started %v, rebuild = id %q project %q started %v",
			got.ID, got.Project, got.Started, want.ID, want.Project, want.Started)
	}
}

func TestCopilotDiscoveryAndProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	writeCopilotFixture(t, root, "s1", copilotFixtureLines())
	// A stray non-events file must not be discovered.
	if err := os.WriteFile(filepath.Join(root, "s1", "workspace.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := CopilotSessionFiles()
	if len(files) != 1 || filepath.Base(files[0]) != "events.jsonl" {
		t.Fatalf("discovery = %v", files)
	}
	if got := LoadCopilot(); len(got) != 1 || got[0].Harness != "copilot" {
		t.Fatalf("LoadCopilot = %#v", got)
	}
	for in, want := range map[string]string{
		"/Users/x/coding/gateway": "coding/gateway",
		"/gateway":                "gateway",
		"/":                       "",
		"":                        "",
	} {
		if got := copilotProjectName(in); got != want {
			t.Fatalf("copilotProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

// One session that works: it speaks, edits a file, runs a command that fails,
// reads a file, and closes. Shapes are the ones the Copilot CLI writes —
// lowercase tool names, `path` rather than `file_path`, `old_str` rather than
// `old_string`, and results under `result.content`.
func copilotWorkLines() []string {
	return []string{
		`{"type":"session.start","data":{"sessionId":"s1","startTime":"2026-07-20T05:13:54.687Z","context":{"cwd":"/Users/x/coding/gateway"}},"timestamp":"2026-07-20T05:13:54.707Z"}`,
		`{"type":"user.message","data":{"content":"the build is broken"},"timestamp":"2026-07-20T05:13:58.653Z"}`,
		`{"type":"assistant.message","data":{"content":"looking at it"},"timestamp":"2026-07-20T05:14:00.973Z"}`,
		`{"type":"tool.execution_start","data":{"toolName":"edit","arguments":{"path":"/Users/x/coding/gateway/main.go","old_str":"println(\"hello\")","new_str":"println(\"goodbye\")"}},"timestamp":"2026-07-20T05:14:01.100Z"}`,
		`{"type":"tool.execution_complete","data":{"success":true,"result":{"content":"1 replacement"}},"timestamp":"2026-07-20T05:14:01.200Z"}`,
		`{"type":"tool.execution_start","data":{"toolName":"bash","arguments":{"command":"go vet ./..."}},"timestamp":"2026-07-20T05:14:02.100Z"}`,
		`{"type":"tool.execution_complete","data":{"success":false,"result":{"content":"pattern ./...: directory prefix . does not contain main module"}},"timestamp":"2026-07-20T05:14:02.900Z"}`,
		`{"type":"tool.execution_start","data":{"toolName":"read","arguments":{"path":"/Users/x/coding/gateway/go.mod"}},"timestamp":"2026-07-20T05:14:03.100Z"}`,
		`{"type":"tool.execution_start","data":{"toolName":"todo","arguments":{"items":[]}},"timestamp":"2026-07-20T05:14:03.500Z"}`,
		`{"type":"session.shutdown","data":{},"timestamp":"2026-07-20T05:14:04.029Z"}`,
	}
}

func TestParseCopilotEmitsWorkRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	p := writeCopilotFixture(t, root, "s1", copilotWorkLines())
	ss, err := ParseCopilotFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}

	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go vet ./..." {
		t.Errorf("commands = %q, want the vet run alone — todo carries none", got)
	}
	wantFiles := []string{"/Users/x/coding/gateway/main.go", "/Users/x/coding/gateway/go.mod"}
	if got := byRole[RoleFiles]; strings.Join(got, ",") != strings.Join(wantFiles, ",") {
		t.Errorf("files = %q, want the edit and the read in order", got)
	}
	// old_str, not old_string: reading the wrong key stores the path with no
	// span, which is the record silently losing what it is for.
	if got := byRole[RoleEdit]; len(got) != 1 ||
		got[0] != "/Users/x/coding/gateway/main.go\nprintln(\"hello\")" ||
		strings.Contains(strings.Join(got, ""), "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", got)
	}
	// The failure is the point: it is what a later search reaches for.
	wantOut := []string{"1 replacement", "pattern ./...: directory prefix . does not contain main module"}
	if got := byRole[RoleToolOutput]; strings.Join(got, "|") != strings.Join(wantOut, "|") {
		t.Errorf("tool output = %q, want both results including the error", got)
	}
}

// With the switches off the stream is what it was before tool events were
// read, so nobody's existing index changes shape under them.
func TestParseCopilotWorkRecordsSwitchOff(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	p := writeCopilotFixture(t, root, "s1", copilotWorkLines())
	ss, err := ParseCopilotFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v", err)
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand, RoleFiles, RoleEdit, RoleToolOutput:
			t.Fatalf("record %q survived its switch", m.Role)
		}
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("want the two spoken turns back, got %d", len(ss[0].Messages))
	}
}
