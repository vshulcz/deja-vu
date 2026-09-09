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
	// The wording these strings were meant to carry: one line, and only when
	// the recall helped. The exact phrasing moved in #3079 — the old one was
	// followed on 143 of 6,650 injections — so the check is on the two
	// properties rather than on one sentence.
	for _, name := range []string{"mcp tools/list", "hook-prompt lead", "hook-context lead", "antigravity lead", "skill guidance"} {
		text := texts[name]
		if !strings.Contains(text, "one short line") && !strings.Contains(text, "one line") {
			t.Errorf("%s: does not ask for one line", name)
		}
		if !strings.Contains(text, "did not help") && !strings.Contains(text, "none helps") &&
			!strings.Contains(text, "ignore silently") {
			t.Errorf("%s: does not say to stay quiet when the recall did not help", name)
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
