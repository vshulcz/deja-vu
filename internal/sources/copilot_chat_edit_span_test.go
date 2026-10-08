package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// copilotChatRequestWithCalls is one chatSessions request whose edits are in
// result.metadata, the way VS Code 1.136 stores agent mode: each call's
// arguments a JSON string, each result keyed by the call's id.
func copilotChatRequestWithCalls(t *testing.T, calls []map[string]any, results map[string]any) map[string]any {
	t.Helper()
	var tc []any
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
	if err := os.WriteFile(p, b, 0o644); err != nil {
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
