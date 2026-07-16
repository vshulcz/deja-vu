package sources

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func grokTree(t *testing.T) (root, updates string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	group := url.PathEscape("/work/cool-app")
	dir := filepath.Join(root, "sessions", group, "019f-grok-session")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(dir, "updates.jsonl")
}

func TestParseGrokFile(t *testing.T) {
	_, updates := grokTree(t)
	summary := `{"info":{"id":"019f-grok-session","cwd":"/work/cool-app"},"session_summary":"Fallback title","generated_title":"Fix the parser","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T10:00:06Z"}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"find grokneedle"},"_meta":{"promptIndex":0}}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first "}},"_meta":{"promptId":"p1"}}}
{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"private reasoning"}}}}
{"timestamp":1782900004,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","content":{"type":"text","text":"tool noise"}}}}
{"timestamp":1782900005,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}},"_meta":{"promptId":"p1"}}}
{malformed
`
	if err := os.WriteFile(updates, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(updates)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "019f-grok-session" || ss[0].Harness != "grok" {
		t.Fatalf("bad session: %#v", ss)
	}
	if ss[0].Project != "cool-app" || ss[0].Title != "Fix the parser" {
		t.Fatalf("bad metadata: %#v", ss[0])
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want 2: %#v", len(ss[0].Messages), ss[0].Messages)
	}
	if ss[0].Messages[0].Role != "user" || ss[0].Messages[0].Text != "find grokneedle" {
		t.Fatalf("bad user message: %#v", ss[0].Messages[0])
	}
	if ss[0].Messages[1].Role != "assistant" || ss[0].Messages[1].Text != "first answer" {
		t.Fatalf("assistant chunks not merged: %#v", ss[0].Messages[1])
	}
}

func TestGrokDiscoveryAndRootOverrides(t *testing.T) {
	root, updates := grokTree(t)
	if err := os.WriteFile(updates, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "chat_history.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := GrokSessionFiles()
	if len(files) != 1 || files[0] != updates {
		t.Fatalf("files = %v, want [%s]", files, updates)
	}

	t.Setenv("DEJA_GROK_ROOT", "")
	t.Setenv("GROK_HOME", root)
	if GrokRoot() != root {
		t.Fatalf("GROK_HOME ignored: %q", GrokRoot())
	}
}

func TestGrokCWDFromEncodedGroupAndMarker(t *testing.T) {
	_, updates := grokTree(t)
	if got := GrokCWDForSession(updates); got != "/work/cool-app" {
		t.Fatalf("encoded cwd = %q", got)
	}
	group := filepath.Dir(filepath.Dir(updates))
	if err := os.WriteFile(filepath.Join(group, ".cwd"), []byte("/a/very/long/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GrokCWDForSession(updates); got != "/a/very/long/project" {
		t.Fatalf("marker cwd = %q", got)
	}
}
