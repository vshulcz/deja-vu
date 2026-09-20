package sources

import (
	"strings"
	"testing"
	"time"
)

// Zed records both sides of an edit, but on the result rather than on the call:
// the `edit_file` input is a path, a mode and a sentence of intent, and the
// change itself comes back under `output` as a unified diff beside the whole
// file before and after. Counted on a real store, 589 of 684 edit_file results
// carry it and not one call did — so 29 of 30 zed sessions had no recorded edit
// at all (#595). Shape from a real thread.
func TestZedEditsComeFromTheToolResult(t *testing.T) {
	const removed = "cfg.MaxConns = 10 // the number from before this change"
	const added = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	msg := `{"Agent":{"content":[{"ToolUse":{"id":"call_1","name":"edit_file","input":{"path":"/w/internal/pool/pool.go","mode":"edit","display_description":"Raise the pool size"}}}],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"edit_file","is_error":false,"content":{"Text":"Edited pool.go"},
	  "output":{"input_path":"/w/internal/pool/pool.go","diff":"@@ -18,7 +18,7 @@\n func open(size int) {\n-` + removed + `\n+` + added + `\n }\n","old_text":"whole file before","new_text":"whole file after"}}}}}`
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

	var replaced, wrote []string
	for _, m := range zedWork([]byte(msg), at) {
		switch m.Role {
		case RoleEdit:
			replaced = append(replaced, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	if len(replaced) != 1 {
		t.Fatalf("replaced spans = %q, want the one the diff removed", replaced)
	}
	path, span, _ := strings.Cut(replaced[0], "\n")
	if path != "/w/internal/pool/pool.go" {
		t.Errorf("the span is filed under %q", path)
	}
	if !strings.Contains(span, removed) {
		t.Errorf("the span is not what the diff removed: %q", span)
	}
	// The whole-file texts beside the diff are 67 KB at their largest on a real
	// store, and they are not what stopped existing at that line.
	if strings.Contains(span, "whole file before") {
		t.Error("the whole file was stored as the replaced span")
	}

	if len(wrote) != 1 {
		t.Fatalf("written records = %q, want one for the diff's added lines", wrote)
	}
	h, ok := HashWrittenLine(added)
	if !ok {
		t.Fatal("the added line is not evidence")
	}
	if _, has := WroteRecordHas(wrote[0], h); !has {
		t.Errorf("the written record does not hold the added line: %q", wrote[0])
	}
	// And not the removed one: that is the other rule's evidence.
	if old, _ := HashWrittenLine(removed); func() bool { _, has := WroteRecordHas(wrote[0], old); return has }() {
		t.Error("a removed line was hashed as written")
	}
}

func TestZedEditsStopWhenSwitchedOff(t *testing.T) {
	msg := `{"Agent":{"content":[],"tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"edit_file",
	  "output":{"input_path":"/w/pool.go","diff":"@@ -1,1 +1,1 @@\n-cfg.MaxConns = 10 // the number before this change\n+cfg.MaxConns = int32(size) // from the ini\n"}}}}}`
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_WRITES", "0")
	for _, m := range zedWork([]byte(msg), at) {
		if m.Role == RoleEdit || m.Role == RoleWrote {
			t.Errorf("a %s record survived with both switches off: %q", m.Role, m.Text)
		}
	}
}

// A result without the diff is not an edit anyone can attribute: 95 of the 684
// on the real store carry no output object at all, and inventing a span from
// the call's own input would file a sentence of intent as the file's old text.
func TestZedSkipsAnEditItCannotRead(t *testing.T) {
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	for name, msg := range map[string]string{
		"the output is a plain string": `{"Agent":{"content":[{"ToolUse":{"id":"call_1","name":"edit_file","input":{"path":"/w/pool.go","mode":"edit","display_description":"Raise the pool size"}}}],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"edit_file","output":"Edited pool.go"}}}}`,
		// Zed writes this one itself: the edit was refused or produced nothing,
		// and the result comes back with the path and an empty diff. A record
		// built from it says a session edited a file and gives nothing to match.
		"the diff is empty": `{"Agent":{"content":[],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"edit_file","output":{"input_path":"/w/pool.go","diff":"","old_text":"whole file","new_text":"whole file"}}}}}`,
	} {
		for _, m := range zedWork([]byte(msg), at) {
			if m.Role == RoleEdit || m.Role == RoleWrote {
				t.Errorf("%s: a %s record was invented: %q", name, m.Role, m.Text)
			}
		}
	}
}

// Only an edit_file result. The filter is on the tool name rather than on the
// shape of the output, so a tool that grows a `diff` field later — zed's own
// `git_state` snapshot already carries one, under another key — does not start
// producing edit records nobody checked.
func TestZedReadsEditsOnlyFromTheEditTool(t *testing.T) {
	msg := `{"Agent":{"content":[],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"terminal",
	  "output":{"input_path":"/w/pool.go","diff":"@@ -1,1 +1,1 @@\n-cfg.MaxConns = 10 // the number before this change\n+cfg.MaxConns = int32(size) // from the ini\n"}}}}}`
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	for _, m := range zedWork([]byte(msg), at) {
		if m.Role == RoleEdit || m.Role == RoleWrote {
			t.Errorf("a %s record came from a %s result: %q", m.Role, "terminal", m.Text)
		}
	}
}
