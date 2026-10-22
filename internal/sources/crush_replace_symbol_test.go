package sources

import (
	"testing"
)

// lsp_replace_symbol's call carries only what it wrote. Its result's metadata,
// ReplaceSymbolResponseMetadata {file_path, old_content, new_content, action},
// holds the file before and after, which is where the replaced symbol is
// (Crush internal/agent/tools/lsp_replace_symbol.go). A failed call and an
// insert after a symbol replaced nothing (#595).
func TestCrushReplaceSymbolRecordsTheReplacedSymbol(t *testing.T) {
	const (
		head   = "package retry\n\nimport \"time\"\n\n"
		before = "func backoffJitter(attempt int) time.Duration {\n\treturn time.Duration(attempt) * time.Second\n}"
		after  = "func backoffJitter(attempt int) time.Duration {\n\treturn time.Duration(rand.Int63n(int64(attempt+1)))\n}"
		tail   = "\n\nfunc keep() {}\n"
	)
	meta := func(path, old, nw, action string) string {
		return vocabJSON(map[string]any{"file_path": path, "old_content": old, "new_content": nw, "action": action})
	}
	call := func(id, path, action string) string {
		return crushInsert(t, "a_"+id, "s1", "assistant", 1784282401, []any{
			map[string]any{"type": "tool_call", "data": map[string]any{"id": "c_" + id, "name": "lsp_replace_symbol",
				"input": vocabJSON(map[string]any{"symbol": "backoffJitter", "file_path": path, "replacement": after, "action": action}), "finished": true}}})
	}
	result := func(id, content, metadata string, isErr bool) string {
		return crushInsert(t, "r_"+id, "s1", "tool", 1784282402, []any{
			map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": "c_" + id, "name": "lsp_replace_symbol",
				"content": content, "metadata": metadata, "is_error": isErr}}})
	}
	sql := "insert into sessions values ('s1',null,'retry',2,0,0,0.0,1784282405,1784282400,null,null);\n" +
		call("1", "/tmp/proj/jitter.go", "replace") +
		result("1", "Replaced symbol 'backoffJitter' in /tmp/proj/jitter.go (lines 5-7)\n<cwd>/tmp/proj</cwd>",
			meta("/tmp/proj/jitter.go", head+before+tail, head+after+tail, "replace"), false) +
		call("2", "/tmp/proj/refused.go", "replace") +
		result("2", "symbol not found", meta("/tmp/proj/refused.go", head+before+tail, head+after+tail, "replace"), true) +
		call("3", "/tmp/proj/added.go", "add_after") +
		result("3", "Inserted after symbol 'backoffJitter' in /tmp/proj/added.go (after line 7)",
			meta("/tmp/proj/added.go", head+before+tail, head+before+"\n"+after+tail, "add_after"), false) +
		call("4", "/tmp/proj/plain.go", "replace") +
		result("4", "Replaced symbol 'backoffJitter'", meta("/tmp/proj/plain.go", head+before+tail, head+after+tail, "replace"), false)
	ss, err := ParseCrushDB(crushStore(t, "proj", sql))
	if err != nil {
		t.Fatal(err)
	}
	// The summary names the lines, so the whole symbol is the span; with no
	// range, the lines the two contents do not share stand in.
	vocabCheck(t, ss, []string{
		vocabEdit("/tmp/proj/jitter.go", before),
		vocabWrote("/tmp/proj/jitter.go", after),
		vocabEdit("/tmp/proj/plain.go", "\treturn time.Duration(attempt) * time.Second"),
	}, []string{
		vocabEdit("/tmp/proj/refused.go", "\treturn time.Duration(attempt) * time.Second"),
	})
	if got := vocabRoles(ss, RoleEdit); len(got) != 2 {
		t.Fatalf("edits = %q, want the two replaced symbols", got)
	}
}
