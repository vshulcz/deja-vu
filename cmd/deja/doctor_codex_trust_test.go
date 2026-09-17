package main

import (
	"strings"
	"testing"
)

// Codex pins trust per hook, so a machine that approved deja's one hook and
// has five now runs one of them — and its own first screen says so while this
// row said `wired` (#3654).
func TestCodexApprovedHooksCountsPinsPerEvent(t *testing.T) {
	cfg := `
[hooks.state."/Users/x/.codex/hooks.json:session_start:0:0"]
trusted_hash = "sha256:abc"
enabled = true
`
	approved, pinned := codexApprovedHooks(cfg, codexHookWiring)
	if pinned != len(codexHookWiring) {
		t.Errorf("pinned = %d, want the %d events deja writes", pinned, len(codexHookWiring))
	}
	if approved != 1 {
		t.Errorf("approved = %d, want the one pin in this config", approved)
	}

	// Every event pinned is the state that may call itself wired without a
	// caveat.
	var all strings.Builder
	for _, h := range codexHookWiring {
		all.WriteString("[hooks.state.\"/Users/x/.codex/hooks.json:" + codexEventKey(h.Event) + ":0:0\"]\n")
		all.WriteString("trusted_hash = \"sha256:abc\"\n")
	}
	if approved, pinned = codexApprovedHooks(all.String(), codexHookWiring); approved != pinned {
		t.Errorf("approved = %d of %d with every event pinned", approved, pinned)
	}
}

// The key is snake_case, which is what makes the count match codex's store
// rather than never matching it.
func TestCodexEventKeyIsSnakeCase(t *testing.T) {
	for event, want := range map[string]string{
		"SessionStart":     "session_start",
		"UserPromptSubmit": "user_prompt_submit",
		"PreToolUse":       "pre_tool_use",
		"PostToolUse":      "post_tool_use",
		"PreCompact":       "pre_compact",
	} {
		if got := codexEventKey(event); got != want {
			t.Errorf("codexEventKey(%q) = %q, want %q", event, got, want)
		}
	}
}
