package sources

import (
	"encoding/json"
	"testing"
)

// A message that carries only tool results is not the user speaking, however
// the harness files it. Measured before this existed: 32,239 of 40,313 records
// under the user role — 80% — were tool output (#559).
func TestToolResultOnlyMessageIsLabelled(t *testing.T) {
	raw := json.RawMessage(`[{"type":"tool_result","content":"ok  github.com/x  1.2s"}]`)
	txt, isTool := claudeTextKind(raw)
	if txt != "ok  github.com/x  1.2s" {
		t.Fatalf("text = %q", txt)
	}
	if !isTool {
		t.Fatal("a tool_result-only message must be labelled as tool output")
	}
}

// Speech wins a mixed message: the person said something and the output rode
// along with it, so attributing the whole turn to a tool would lose the words.
func TestMixedMessageStaysSpeech(t *testing.T) {
	raw := json.RawMessage(`[
	  {"type":"text","text":"here is what I ran"},
	  {"type":"tool_result","content":"exit status 1"}
	]`)
	if _, isTool := claudeTextKind(raw); isTool {
		t.Fatal("a message containing speech must not be labelled tool output")
	}
}

func TestPlainAndEmptyShapesAreSpeech(t *testing.T) {
	for _, raw := range []string{`"just a sentence"`, `[]`, `null`, `[{"type":"text","text":"hi"}]`} {
		if _, isTool := claudeTextKind(json.RawMessage(raw)); isTool {
			t.Fatalf("%s was labelled tool output", raw)
		}
	}
}

// An MCP tool's result arrives as an array of content blocks, not a string —
// {"type":"tool_result","content":[{"type":"text","text":…}]} — and both
// parsers dropped the item whole: the text never reached the index and the
// label never applied. Measured on one real store: 76 records lost that way.
func TestToolResultContentBlocksAreKept(t *testing.T) {
	line := `[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"3 tests failed"},{"type":"image","source":{"type":"base64"}},{"type":"text","text":"in auth_test.go"}]}]`
	txt, isTool := claudeTextKind(json.RawMessage(line))
	if txt != "3 tests failed\nin auth_test.go" {
		t.Fatalf("text = %q", txt)
	}
	if !isTool {
		t.Fatal("a block-array tool result must be labelled as tool output")
	}
	var v any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		t.Fatal(err)
	}
	gtxt, gTool := textFromContentKind(v)
	if gtxt != txt || gTool != isTool {
		t.Fatalf("generic (%q, %v) disagrees with typed (%q, %v)", gtxt, gTool, txt, isTool)
	}
}

// Speech still wins a mixed message when the tool result is block-shaped.
func TestMixedMessageWithBlockResultStaysSpeech(t *testing.T) {
	line := `[{"type":"text","text":"ran the suite"},{"type":"tool_result","content":[{"type":"text","text":"ok"}]}]`
	txt, isTool := claudeTextKind(json.RawMessage(line))
	if isTool {
		t.Fatal("a message containing speech must not be labelled tool output")
	}
	if txt != "ran the suite\nok" {
		t.Fatalf("text = %q", txt)
	}
}

// An image-only result has nothing worth indexing and must stay empty rather
// than become a blank record — but it is still a tool result for the label.
func TestImageOnlyBlockResultStaysEmpty(t *testing.T) {
	txt, isTool := claudeTextKind(json.RawMessage(`[{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"AAAA"}}]}]`))
	if txt != "" {
		t.Fatalf("text = %q, want empty", txt)
	}
	if !isTool {
		t.Fatal("still a tool result, even with nothing to keep")
	}
}

// The content field takes junk shapes too — numbers, objects, bare strings in
// the block list, broken nesting. They must cost nothing, and the two parsers
// must read them identically.
func TestContentBlockJunkIsTolerated(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`123`, ""},
		{`{"nested":"object"}`, ""},
		{`["a bare string in the blocks"]`, ""},
		{`[{"type":"text","text":123}]`, ""},
		{`[{"type":"text"},{"type":"text","text":"kept"}]`, "kept"},
		{`[not json`, ""},
		{`"unterminated`, ""},
		{`"plain"`, "plain"},
	}
	for _, c := range cases {
		if got := claudeContentText(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("typed claudeContentText(%s) = %q, want %q", c.raw, got, c.want)
		}
		var v any
		if json.Unmarshal([]byte(c.raw), &v) != nil {
			v = nil // undecodable content never reaches the generic parser as a value
		}
		if got := contentText(v); got != c.want {
			t.Errorf("generic contentText(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// The reference parser must agree with the typed one, or the differential test
// in #502 stops meaning anything.
func TestBothParsersAgreeOnTheLabel(t *testing.T) {
	var v any
	if err := json.Unmarshal([]byte(`[{"type":"tool_result","content":"boom"}]`), &v); err != nil {
		t.Fatal(err)
	}
	_, generic := textFromContentKind(v)
	_, typed := claudeTextKind(json.RawMessage(`[{"type":"tool_result","content":"boom"}]`))
	if generic != typed || !generic {
		t.Fatalf("generic=%v typed=%v, want both true", generic, typed)
	}
}
