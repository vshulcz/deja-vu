package sources

import (
	"encoding/json"
	"testing"
)

func TestClaudeTextKind(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantText string
		wantTool bool
	}{
		{"plain string", `"hello there"`, "hello there", false},
		{"text part", `[{"type":"text","text":"hi"}]`, "hi", false},
		{"tool result string", `[{"type":"tool_result","content":"the output"}]`, "the output", true},
		{"tool result mcp blocks", `[{"type":"tool_result","content":[{"type":"text","text":"mcp out"},{"type":"image"}]}]`, "mcp out", true},
		{"mixed counts as speech", `[{"type":"text","text":"do it"},{"type":"tool_result","content":"result"}]`, "do it\nresult", false},
		{"empty", ``, "", false},
		{"scalar non-string", `123`, "", false},
		{"malformed array", `[{bad}]`, "", false},
	}
	for _, c := range cases {
		gotText, gotTool := claudeTextKind(json.RawMessage(c.raw))
		if gotText != c.wantText || gotTool != c.wantTool {
			t.Errorf("%s: claudeTextKind(%s) = (%q, %v), want (%q, %v)", c.name, c.raw, gotText, gotTool, c.wantText, c.wantTool)
		}
	}
}

func TestClaudeTextReturnsJustText(t *testing.T) {
	if got := claudeText(json.RawMessage(`[{"type":"tool_result","content":"x"}]`)); got != "x" {
		t.Fatalf("claudeText = %q, want x", got)
	}
}

func TestHarnessRegistryHelpers(t *testing.T) {
	names := HarnessNames()
	if len(names) == 0 {
		t.Fatal("HarnessNames returned nothing")
	}
	found := false
	for _, n := range names {
		if n == "claude" {
			found = true
		}
		if !IsKnownHarness(n) {
			t.Errorf("HarnessNames listed %q but IsKnownHarness says no", n)
		}
	}
	if !found {
		t.Errorf("claude not among harnesses: %v", names)
	}
	if IsKnownHarness("definitely-not-a-harness") {
		t.Error("IsKnownHarness accepted an unknown name")
	}
}
