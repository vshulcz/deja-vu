package sources

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// Cases found on a real Linux 4.x store: conversations with no time, the
// same conversation in two workspaces, files a compaction deleted, and tool
// results only the converter's JSON fallback reads.

// A conversation that carries no time anywhere — no createdAt on its
// messages, no startedAt on its requests, no entry in the workspace index —
// is dated by its index file. On a real store four were, and each sorted as
// "-" in deja last and fell outside every --since window.
func TestParseCodeBuddyIDE4UndatedConversationTakesTheIndexTime(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Index(t, c, []any{cb4Request([]string{"m01", "m02"}, nil, nil)})
	cb4Message(t, c, "m01", "user", []any{cb4Text(cb4UserQuery("/work/demo", "an undated question"))}, nil, nil)
	cb4Message(t, c, "m02", "assistant", []any{cb4Text("an undated answer")}, nil, nil)
	at := time.Date(2026, 3, 4, 9, 10, 0, 0, time.UTC)
	if err := os.Chtimes(c.Index, at, at); err != nil {
		t.Fatal(err)
	}
	s := cb4ParseOne(t, c.Index)
	if !s.Started.Equal(at) || !s.Updated.Equal(at) {
		t.Errorf("started %v, updated %v; want the index file's time %v", s.Started, s.Updated, at)
	}

	// A conversation that does carry a time keeps it: the file's time is a
	// fallback, not a bound.
	dated := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0002")
	cb4Index(t, dated, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
	cb4Message(t, dated, "m01", "user", []any{cb4Text("a dated question")}, nil, nil)
	if err := os.Chtimes(dated.Index, at.Add(time.Hour), at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	d := cb4ParseOne(t, dated.Index)
	if got := d.Started.UTC().Format(time.RFC3339); got != cb4At {
		t.Errorf("dated conversation started %s, want its request's %s", got, cb4At)
	}
	if got := d.Updated.UTC().Format(time.RFC3339); got != cb4At {
		t.Errorf("dated conversation updated %s, want its request's %s", got, cb4At)
	}
}

// The IDE can hold one conversation in two workspace directories, byte for
// byte: a Linux store kept 204 that way. The walk lists it once — the copy
// whose index was written last, else the smaller path — and hands the other
// back as a copy, so it is neither read twice nor reported as unplaced.
func TestCodeBuddyIDE4SessionFilesListsACopiedConversationOnce(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	a := cb4Minimal(t, root)
	b := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0002", a.ID)
	cb4Workspace(t, b, cb4Entry(b.ID, "craft", "Fix the retry loop"))
	cb4Index(t, b, []any{cb4Request([]string{"m01", "m02"}, cb4StartMS, nil)})
	cb4Message(t, b, "m01", "user", []any{cb4Text(cb4UserQuery("/work/retry-demo", "Fix the retry off-by-one"))}, nil, cb4At)
	cb4Message(t, b, "m02", "assistant", []any{cb4Text("I will fix the loop.")}, map[string]any{"modelId": "claude-synthetic-4"}, cb4LaterAt)
	other := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0002", "conv0002")
	cb4Index(t, other, []any{cb4Request([]string{"m01"}, cb4StartMS, nil)})
	cb4Message(t, other, "m01", "user", []any{cb4Text("another conversation")}, nil, cb4At)

	stamp := func(p string, at time.Time) {
		t.Helper()
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name         string
		aAt, bAt     time.Time
		want, copied string
	}{
		{"the later index is read", at, at.Add(time.Minute), b.Index, a.Index},
		{"either way round", at.Add(time.Minute), at, a.Index, b.Index},
		{"at the same time, the smaller path", at, at, a.Index, b.Index},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stamp(a.Index, tc.aAt)
			stamp(b.Index, tc.bAt)
			files, copies := CodeBuddyIDE4SessionFilesAndCopies()
			slices.Sort(files)
			want := []string{tc.want, other.Index}
			slices.Sort(want)
			if !slices.Equal(files, want) {
				t.Errorf("files = %v, want %v", files, want)
			}
			if !slices.Equal(copies, []string{tc.copied}) {
				t.Errorf("copies = %v, want [%s]", copies, tc.copied)
			}
			if got := CodeBuddyIDE4SessionFiles(); len(got) != 2 {
				t.Errorf("CodeBuddyIDE4SessionFiles = %v, want two conversations", got)
			}
			ids := map[string]int{}
			for _, s := range LoadCodeBuddyIDE4() {
				ids[s.ID]++
			}
			if ids[a.ID] != 1 || ids[other.ID] != 1 {
				t.Errorf("loaded %v, want each conversation once", ids)
			}
		})
	}
}

// The IDE deletes the files of the turns a compaction summary replaced and
// keeps their ids in the conversation index. Those are not records deja
// failed to read: on a real store they were 52 of the 59 missing files that
// made every full index exit as partial. A file missing after the last
// summary, or in a conversation with none, is still reported.
func TestParseCodeBuddyIDE4FilesACompactionDeletedAreNotUnusable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary map[string]any
	}{
		{"max-token summary", map[string]any{"isMaxTokenLimitSummary": true, "isSummary": true, "originalSummary": "the turns so far"}},
		{"summary prompt", map[string]any{"isSummary": true, "isCompacted": true}},
		{"compacted prompt", map[string]any{"isCompacted": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cb4Env(t)
			root := CodeBuddyIDE4DefaultRoot()
			c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
			// m01..m03 were compacted away; m04 carries the summary; m06 is
			// missing after it.
			cb4Index(t, c, []any{
				cb4Request([]string{"m01", "m02", "m03"}, cb4StartMS, nil),
				cb4Request([]string{"m04", "m05", "m06", "m07"}, cb4MiddleMS, nil),
			})
			cb4Message(t, c, "m03", "assistant", []any{cb4Text("kept before the summary")}, nil, cb4At)
			cb4Message(t, c, "m04", "user", []any{cb4Text("<cb_summary>the turns so far</cb_summary>\n<user_query>\nnext question\n</user_query>")}, tc.summary, cb4LaterAt)
			cb4Message(t, c, "m05", "assistant", []any{cb4Text("next answer")}, nil, cb4LaterAt)
			cb4Message(t, c, "m07", "assistant", []any{cb4Text("last answer")}, nil, cb4EndAt)
			state := DiagSave()
			defer DiagRestore(state)
			before := DiagMalformedCounts()[c.Index]
			s := cb4ParseOne(t, c.Index)
			if !cb4Has(cb4Roles(s), "assistant", "next answer") || !cb4Has(cb4Roles(s), "assistant", "kept before the summary") {
				t.Errorf("messages = %v", cb4Roles(s))
			}
			if got := DiagMalformedCounts()[c.Index] - before; got != 1 {
				t.Errorf("unusable records = %d, want only the file missing after the summary", got)
			}
			reasons := DiagUnusableRecords()[c.Index]
			if _, ok := reasons["m06"]; !ok || len(reasons) != 1 {
				t.Errorf("unusable records = %v, want m06 alone", reasons)
			}
		})
	}

	// With no summary, every missing file is reported as before.
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0002")
	cb4Index(t, c, []any{cb4Request([]string{"m01", "m02", "m03"}, cb4StartMS, nil)})
	cb4Message(t, c, "m03", "user", []any{cb4Text("a question")}, map[string]any{"isCompacted": false}, cb4At)
	state := DiagSave()
	defer DiagRestore(state)
	before := DiagMalformedCounts()[c.Index]
	cb4ParseOne(t, c.Index)
	if got := DiagMalformedCounts()[c.Index] - before; got != 2 {
		t.Errorf("unusable records without a summary = %d, want both missing files", got)
	}
}

// A tool result with none of the text fields the reader knows is what the
// 5.x converter writes as JSON with sorted keys (craftToolResultText,
// stableJson). Read here as nothing, an edit, a write or a file search left
// no output before the upgrade and one after: a real conversation read 6
// outputs from its 4.x copy and 15 from its transcript.
func TestCodeBuddyIDE4ToolResultFallsBackToTheConvertersJSON(t *testing.T) {
	cb4Env(t)
	root := CodeBuddyIDE4DefaultRoot()
	c := cb4New(t, root, "userA/CodeBuddyIDE/userA/history", "ws0001", "conv0001")
	cb4Index(t, c, []any{cb4Request([]string{"m01", "m02", "m03"}, cb4StartMS, nil)})
	cb4Message(t, c, "m01", "user", []any{cb4Text(cb4UserQuery("/work/demo", "fix it"))}, nil, cb4At)
	cb4Message(t, c, "m02", "assistant", []any{
		cb4ToolCall("e1", "replace_in_file", map[string]any{"filePath": "/work/demo/a.go", "old_str": "a", "new_str": "b"}),
		cb4ToolCall("w1", "write_to_file", map[string]any{"filePath": "/work/demo/<b>.md", "content": "x"}),
		cb4ToolCall("l1", "lsp", map[string]any{"operation": "documentSymbol"}),
		cb4ToolCall("r1", "read_lints", map[string]any{}),
		cb4ToolCall("i1", "read_file", map[string]any{"filePath": "/work/demo/shot.png"}),
		cb4ToolCall("p1", "plan_update", map[string]any{}),
	}, nil, cb4LaterAt)
	cb4Message(t, c, "m03", "tool", []any{
		cb4ToolResult("e1", "replace_in_file", cb4OKResult(map[string]any{
			"type": "replace_in_file_result", "path": "/work/demo/a.go", "matchCount": 1.0, "addLineCount": 2.0,
		}), false),
		cb4ToolResult("w1", "write_to_file", cb4OKResult(map[string]any{
			"type": "write_to_file_result", "path": "/work/demo/<b>.md", "isNewFile": true,
		}), false),
		cb4ToolResult("l1", "lsp", cb4OKResult(map[string]any{
			"type": "lsp_tool_result", "operation": "documentSymbol", "result": "retry (Function) [3:1]",
		}), false),
		// The converter formats a lint result from its diagnostics alone, and
		// an empty list leaves an empty output; an image is not indexed.
		cb4ToolResult("r1", "read_lints", cb4OKResult(map[string]any{
			"type": "read_lints_result", "diagnostics": []any{""}, "totalCount": 0.0,
		}), false),
		cb4ToolResult("i1", "read_file", map[string]any{"status": "success", "result": []any{
			map[string]any{"type": "image_blob_ref", "blob_id": "b1"},
		}}, false),
		cb4ToolResult("p1", "plan_update", map[string]any{"status": "error", "success": false, "errorCode": "12300"}, true),
	}, nil, cb4EndAt)
	s := cb4ParseOne(t, c.Index)
	var outputs []string
	for _, m := range s.Messages {
		if m.Role == RoleToolOutput {
			outputs = append(outputs, m.Text)
		}
	}
	want := []string{
		`{"addLineCount":2,"matchCount":1,"path":"/work/demo/a.go","type":"replace_in_file_result"}`,
		`{"isNewFile":true,"path":"/work/demo/<b>.md","type":"write_to_file_result"}`,
		`{"operation":"documentSymbol","result":"retry (Function) [3:1]","type":"lsp_tool_result"}`,
		`{"errorCode":"12300","status":"error","success":false}`,
	}
	if !slices.Equal(outputs, want) {
		t.Errorf("tool outputs:\n%s\nwant:\n%s", strings.Join(outputs, "\n"), strings.Join(want, "\n"))
	}
	// A result with a text field keeps that text.
	if got := codeBuddyIDE4ToolOutput("execute_command", cb4OKResult(map[string]any{"stdout": "ok"})); got != "ok" {
		t.Errorf("a shell result read as %q", got)
	}
	if got := codeBuddyIDE4ToolOutput("custom", "plain text"); got != "plain text" {
		t.Errorf("a string result read as %q", got)
	}
	if got := codeBuddyIDE4ToolOutput("custom", nil); got != "" {
		t.Errorf("no result read as %q", got)
	}
	if got := codeBuddyIDE4ToolOutput("custom", map[string]any{"result": 7.0}); got != "7" {
		t.Errorf("a number result read as %q", got)
	}
	if got := codeBuddyIDE4ToolOutput("custom", []any{map[string]any{"type": "image", "data": "x"}}); got != "" {
		t.Errorf("an image list read as %q", got)
	}
}

// For a tool it renames, the converter writes its own text where the reader
// finds none (remapResult), and only falls back to the JSON when its
// formatter gives up. Each case is the shape a real 4.x store held.
func TestCodeBuddyIDE4ToolOutputFollowsTheConvertersFormatters(t *testing.T) {
	ok := cb4OKResult
	for _, tc := range []struct {
		name, tool string
		res        any
		want       string
	}{
		{"a silent command", "execute_command", ok(map[string]any{"type": "execute_command_result", "stdout": "", "stderr": "", "exitCode": 0.0}),
			"Stdout: (empty)\nStderr: (empty)\nExit Code: 0\nSignal: (none)"},
		// The error the reader already finds is kept: one output either way.
		{"a silent failing command", "execute_command", map[string]any{"status": "error", "errorMessage": "exit 3", "result": map[string]any{"type": "execute_command_result", "stdout": "", "exitCode": 3.0}},
			"exit 3"},
		{"a search with no match", "search_content", ok(map[string]any{"type": "search_content_result", "pattern": "x", "matches": []any{}}), "No matches found"},
		{"a file search with no match", "search_content", ok(map[string]any{"type": "search_content_result", "outputMode": "files_with_matches"}), "No files found"},
		// Matches that carry text are read as the text, one output as the
		// converter writes one; matches without it, a line a match as the
		// converter writes them.
		{"matching lines with text", "search_content", ok(map[string]any{"type": "search_content_result", "matches": []any{
			map[string]any{"filePath": "a.go", "startLine": 12.0, "content": "func retry() {"},
		}}), "func retry() {"},
		{"matching lines without text", "search_content", ok(map[string]any{"type": "search_content_result", "matches": []any{
			map[string]any{"filePath": "a.go", "startLine": 12.0, "content": " \r\n "},
			map[string]any{"filePath": "b.go"},
			map[string]any{"content": ""},
		}}), "a.go:12: \nb.go:"},
		{"matching files", "search_content", ok(map[string]any{"type": "search_content_result", "outputMode": "files_with_matches", "matches": []any{
			map[string]any{"filePath": "a.go"}, map[string]any{"filePath": "b.go"},
		}}), "a.go\nb.go"},
		{"match counts", "search_content", ok(map[string]any{"type": "search_content_result", "outputMode": "count", "matches": []any{
			map[string]any{"filePath": "a.go"}, map[string]any{"filePath": "b.go", "content": " "},
		}}), "a.go:1\nb.go:1"},
		{"a search shape the converter does not know", "search_content", ok(map[string]any{"type": "other", "x": 1.0}), `{"type":"other","x":1}`},
		{"a file search", "search_file", ok(map[string]any{"type": "search_file_result", "results": []any{
			map[string]any{"filePath": "references/guide.md", "size": "22.0 KB"},
			map[string]any{"filePath": "README.md"},
		}}), "references/guide.md\nREADME.md"},
		{"a listing", "list_files", ok(map[string]any{"type": "list_files_result", "files": []any{
			map[string]any{"filePath": ".gitignore", "size": "87 B"},
		}}), ".gitignore"},
		{"an empty listing", "list_dir", ok(map[string]any{"type": "list_files_result", "files": []any{}}), "No files found"},
		{"a web search whose passages the converter does not read", "web_search", ok(map[string]any{"type": "web_search_tool_result", "data": []any{
			map[string]any{"passage": "a passage"},
		}}), "No search results found for query: \"\""},
		{"a web search", "web_search", ok(map[string]any{"type": "web_search_tool_result", "query": "q", "results": []any{
			map[string]any{"title": "T", "url": "https://example.com", "snippet": "S"},
		}}), "# Search Results for \"q\"\n\n## 1. [T](https://example.com)\n\nS\n\n**URL:** https://example.com\n\n---"},
		{"an answered question", "ask_followup_question", ok(map[string]any{"type": "multi_question_result",
			"questions": []any{
				map[string]any{"id": "q1", "question": "Which engine?"}, map[string]any{"id": "q2", "question": "Unanswered?"},
				map[string]any{"id": "q3", "question": "Typed?"}, "not a question",
			},
			"answers": map[string]any{"q1": []any{"Ghidra", " ", 3.0, "Hopper"}, "q3": " by hand "},
		}), " · Which engine? → Ghidra, Hopper\n · Typed? → by hand"},
		{"a single question's result", "ask_followup_question", ok(map[string]any{"type": "followup_question_result", "question": "Go on?"}),
			`{"question":"Go on?","type":"followup_question_result"}`},
		{"a lint run with no diagnostics", "read_lints", ok(map[string]any{"type": "read_lints_result", "diagnostics": []any{""}}), ""},
		{"a lint result the formatter gives up on", "read_lints", ok(map[string]any{"type": "read_lints_result", "totalCount": 0.0}),
			`{"totalCount":0,"type":"read_lints_result"}`},
		{"a tool the converter does not rename", "delete_file", ok(map[string]any{"type": "delete_file_result", "path": "/w/a.py", "recursive": false}),
			`{"path":"/w/a.py","recursive":false,"type":"delete_file_result"}`},
		{"a result with no result field", "delete_file", map[string]any{"status": "error", "success": false}, `{"status":"error","success":false}`},
		{"a result that is a list", "custom", []any{1.0, map[string]any{"b": 2.0, "a": 1.0}}, `[1,{"a":1,"b":2}]`},
		{"an empty command result", "execute_command", ok(map[string]any{"type": "other"}), `{"type":"other"}`},
		{"a read of an image", "read_file", ok(map[string]any{"type": "read_file_result", "content": "", "hasMore": false,
			"image": map[string]any{"data": "/9j/4AAQSkZJRg", "mimeType": "image/jpeg"}}), ""},
		{"index keys first, as JavaScript orders them", "RAG_lookup", ok(map[string]any{"b": 1.0, "10": "x", "2": "y", "a": 2.0}),
			`{"2":"y","10":"x","a":2,"b":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := codeBuddyIDE4ToolOutput(tc.tool, tc.res); got != tc.want {
				t.Errorf("output = %q, want %q", got, tc.want)
			}
		})
	}
}

// stableJson's strings are JSON.stringify's: only the quote, the backslash and
// control characters are escaped, so a path with < or & reads as written.
func TestCodeBuddyIDE4StableJSONStrings(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{"a <b> & \"c\" \\ d\n\t\r\b\f\x01", `"a <b> & \"c\" \\ d\n\t\r\b\f\u0001"`},
		{[]any{1.5, -2.0, true, nil, "é"}, `[1.5,-2,true,null,"é"]`},
		{map[string]any{}, `{}`},
	} {
		if got := codeBuddyIDE4StableJSON(tc.v); got != tc.want {
			t.Errorf("stableJson(%#v) = %s, want %s", tc.v, got, tc.want)
		}
	}
}
