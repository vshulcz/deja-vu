package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func grokSessionWith(t *testing.T, lines string) []string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions", "proj", "01a00feb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"01a00feb","cwd":"/work/app"},"generated_title":"Fix the build","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1", len(ss))
	}
	var out []string
	for _, m := range ss[0].Messages {
		out = append(out, m.Role+": "+m.Text)
	}
	return out
}

// Grok writes the whole run into updates.jsonl and the parser took only the
// talk, so `--role tool` found nothing and `deja show` returned a conversation
// that looked complete (#1321). The real events carry their output in an ACP
// content array.
func TestGrokToolCallsAreIndexed(t *testing.T) {
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"why does the build fail"},"_meta":{"promptIndex":0}}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"go build ./...","kind":"execute","status":"pending"}}}
{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"call_1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"undefined: parseThing"}}]}}}
{"timestamp":1782900004,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"the helper was renamed"}},"_meta":{"promptId":"p1"}}}
`
	got := grokSessionWith(t, lines)
	want := []string{
		"user: why does the build fail",
		"tool-output: execute: go build ./...",
		"tool-output: undefined: parseThing",
		"assistant: the helper was renamed",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The private reasoning stream stays out, as it always has.
func TestGrokThoughtsStayOut(t *testing.T) {
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"private reasoning"}}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"said out loud"}},"_meta":{"promptId":"p1"}}}
`
	for _, line := range grokSessionWith(t, lines) {
		if strings.Contains(line, "private reasoning") {
			t.Errorf("thought chunk was indexed: %q", line)
		}
	}
}

// A streamed reply is joined into one message, and a tool call landing between
// its chunks must not split it.
func TestGrokChunksStillJoinAcrossAToolCall(t *testing.T) {
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first "}},"_meta":{"promptId":"p1"}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"ls","kind":"execute"}}}
{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"second"}},"_meta":{"promptId":"p1"}}}
`
	got := grokSessionWith(t, lines)
	want := []string{"assistant: first second", "tool-output: execute: ls"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Two replies streaming at once: the join keys on the prompt id, so chunks of
// different replies stay separate. What must not happen is a chunk landing in
// the wrong message — the join now writes into a remembered index rather than
// into whatever was appended last.
func TestGrokInterleavedRepliesDoNotMix(t *testing.T) {
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"one "}},"_meta":{"promptId":"p1"}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"two "}},"_meta":{"promptId":"p2"}}}
{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"ls","kind":"execute"}}}
{"timestamp":1782900004,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"two more"}},"_meta":{"promptId":"p2"}}}
`
	got := grokSessionWith(t, lines)
	want := []string{
		"assistant: one ",
		"assistant: two two more",
		"tool-output: execute: ls",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
