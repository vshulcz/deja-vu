package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Roo Code and Cline append an <environment_details> block — visible files,
// open tabs, the clock, the cost, the mode, a workspace listing — to every
// user turn. It is the host's, not the person's, and it went into the index
// inside the user message (#3255).
func TestRooEnvironmentDetailsIsNotTheUsersText(t *testing.T) {
	env := "<environment_details>\n# VSCode Visible Files\ninternal/retry/retry.go\n\n# Current Time\n" +
		"Current time in ISO 8601 UTC format: 2026-09-08T01:33:20.000Z\n\n# Current Mode\n<slug>code</slug>\n<name>Code</name>\n</environment_details>"
	turns := []map[string]any{
		{"role": "user", "content": []map[string]string{
			{"type": "text", "text": "<task>\nfix the flaky vantrell retry\n</task>"},
			{"type": "text", "text": env},
		}},
		{"role": "assistant", "content": []map[string]string{{"type": "text", "text": "pinned the deadline per attempt"}}},
		{"role": "user", "content": []map[string]string{
			{"type": "text", "text": "also cover it with a test"},
			{"type": "text", "text": env},
		}},
	}
	dir := filepath.Join(t.TempDir(), "tasks", "1757300000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(turns)
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseRooTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	want := []string{"fix the flaky vantrell retry", "pinned the deadline per attempt", "also cover it with a test"}
	if len(ss[0].Messages) != len(want) {
		t.Fatalf("messages = %+v", ss[0].Messages)
	}
	for i, m := range ss[0].Messages {
		if m.Text != want[i] {
			t.Errorf("message %d = %q, want %q", i, m.Text, want[i])
		}
	}
}

// A person's sentence about the block stays; only the block itself goes.
func TestUnwrapClineTaskKeepsWordsAroundTheBlock(t *testing.T) {
	got := unwrapClineTask("<task>\nwhy is <environment_details> in my history\n</task>\n<environment_details>\n# Current Cost\n$0.12\n</environment_details>")
	if got != "why is <environment_details> in my history" {
		t.Errorf("got %q", got)
	}
}

// A store written with CRLF carries the block with \r before every \n.
func TestUnwrapClineTaskDropsACRLFBlock(t *testing.T) {
	got := unwrapClineTask("<task>\r\nfix the build\r\n</task>\r\n<environment_details>\r\n# Current Cost\r\n$0.12\r\n</environment_details>\r\n")
	if got != "fix the build" {
		t.Errorf("got %q", got)
	}
}
