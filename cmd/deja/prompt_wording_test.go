package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Everything in here is read by a model, not by a human: the MCP tool
// descriptions, the two hook leads, the antigravity lead and the installed
// skill. A stray Go identifier in that text is a prompt bug.
func TestModelFacingTextHasNoStrayIdentifiers(t *testing.T) {
	res, code, msg := handleMCP(t.TempDir(), rpcRequest{Method: "tools/list"})
	if code != 0 {
		t.Fatalf("tools/list: %d %s", code, msg)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{
		"mcp tools/list":    string(raw),
		"hook-prompt lead":  promptHookLead,
		"hook-context lead": sessionStartLead,
		"antigravity lead":  antigravityLead,
		"skill guidance":    guidanceBody,
	}
	for name, text := range texts {
		if strings.Contains(text, "digest.Short") {
			t.Errorf("%s: model-facing text contains the identifier %q: %s", name, "digest.Short", excerpt(text, "digest.Short"))
		}
	}
	// The wording these strings were meant to carry.
	for _, name := range []string{"mcp tools/list", "hook-prompt lead", "hook-context lead", "antigravity lead", "skill guidance"} {
		if !strings.Contains(texts[name], "in one short line") {
			t.Errorf("%s: missing the %q instruction", name, "in one short line")
		}
	}
}

func excerpt(text, needle string) string {
	i := strings.Index(text, needle)
	if i < 0 {
		return ""
	}
	start := max(i-60, 0)
	end := min(i+len(needle)+60, len(text))
	return "..." + text[start:end] + "..."
}
