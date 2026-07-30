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
