package sources

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// grokLineageSession writes summary.json and updates.jsonl for one session
// under root and returns the updates path.
func grokLineageSession(t *testing.T, root, id, summary, updates string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", url.PathEscape("/work/ledger"), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(p, []byte(updates), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Grok Build 1.0.41: a --fork-session copy names its source and forked_at, a
// subagent names no parent and its parent's subagent_spawned record names
// both ends.
func TestGrokForkAndSubagentLineage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	turn := `{"timestamp":1,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}` + "\n"
	spawn := `{"timestamp":2,"method":"_x.ai/session/update","params":{"update":{"sessionUpdate":"subagent_spawned","parent_session_id":"p1","child_session_id":"c1"}}}` + "\n"

	grokLineageSession(t, root, "p1", `{"info":{"id":"p1","cwd":"/work/ledger"},"session_kind":"headless","created_at":"`+at(-time.Hour)+`"}`, turn+spawn)
	// A session created after the child cannot hold its spawn record.
	grokLineageSession(t, root, "late", `{"info":{"id":"late","cwd":"/work/ledger"},"created_at":"`+at(time.Hour)+`"}`,
		strings.ReplaceAll(spawn, `"p1"`, `"late"`))
	child := grokLineageSession(t, root, "c1", `{"info":{"id":"c1","cwd":"/work/ledger"},"session_kind":"subagent","created_at":"`+at(-time.Minute)+`"}`, turn)
	orphan := grokLineageSession(t, root, "c2", `{"info":{"id":"c2","cwd":"/work/ledger"},"session_kind":"subagent_fork","created_at":"`+at(-time.Minute)+`"}`, turn)
	fork := grokLineageSession(t, root, "f1", `{"info":{"id":"f1","cwd":"/work/ledger"},"session_kind":"headless","parent_session_id":"p1","forked_at":"`+at(-time.Minute)+`"}`, turn)

	for _, tc := range []struct{ path, kind, parent string }{
		{child, "subagent", "p1"},
		{orphan, "subagent_fork", ""},
		{fork, "fork", "p1"},
	} {
		ss, err := ParseGrokFile(tc.path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %v %#v", tc.path, err, ss)
		}
		if ss[0].Kind != tc.kind || ss[0].Parent != tc.parent {
			t.Errorf("%s read as kind %q parent %q, want %q %q", filepath.Base(filepath.Dir(tc.path)), ss[0].Kind, ss[0].Parent, tc.kind, tc.parent)
		}
	}
	// Found once, the parent is served from memory.
	if p := grokSpawner(child, "c1", ""); p != "p1" {
		t.Errorf("grokSpawner(c1) = %q, want p1", p)
	}
	if p := grokSpawner(child, "", ""); p != "" {
		t.Errorf("grokSpawner with no child id = %q", p)
	}

	// By id alone, as a prompt hook or MCP call names the asker.
	if s, ok := GrokSessionHead("c1", ""); !ok || s.Parent != "p1" || s.Kind != "subagent" {
		t.Errorf("GrokSessionHead(c1) = %+v %v", s, ok)
	}
	if s, ok := GrokSessionHead("f1", fork); !ok || s.Parent != "p1" || s.Kind != "fork" {
		t.Errorf("GrokSessionHead(f1, transcript) = %+v %v", s, ok)
	}
	for _, id := range []string{"", " ", "nope", "..", "../c1", "p1"} {
		transcript := ""
		if id == "p1" {
			transcript = child // names another session's file
		}
		if s, ok := GrokSessionHead(id, transcript); ok && id != "p1" {
			t.Errorf("GrokSessionHead(%q) = %+v, want none", id, s)
		} else if id == "p1" && ok {
			t.Errorf("GrokSessionHead(p1) took c1's summary: %+v", s)
		}
	}
	if IsGrokTranscript(filepath.Join(filepath.Dir(child), "chat_history.jsonl")) {
		t.Error("chat_history.jsonl read as a grok transcript")
	}
}

// chat_history.jsonl is what Grok sends the model; text a message carried is
// in it JSON-encoded once, and a tool output quoting it is encoded twice.
func TestGrokChatHolds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	updates := grokLineageSession(t, root, "s1", `{"info":{"id":"s1","cwd":"/work/ledger"}}`, "")
	chat := filepath.Join(filepath.Dir(updates), "chat_history.jsonl")
	marker := "<deja-recall>\nThe sessions below are "
	if GrokChatHolds(updates, marker) {
		t.Error("no chat_history.jsonl, yet it holds the marker")
	}
	// Past the first read chunk, so a match across the boundary is found.
	pad := bytes.Repeat([]byte("x"), 256<<10-10)
	line := `{"type":"user","content":[{"type":"text","text":"` + string(pad) + `<deja-recall>\nThe sessions below are here"}]}` + "\n"
	if err := os.WriteFile(chat, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if !GrokChatHolds(updates, marker) {
		t.Error("the marker across a read boundary was missed")
	}
	quoted := `{"type":"tool_result","content":"{\"text\":\"<deja-recall>\\nThe sessions below are \"}"}` + "\n"
	if err := os.WriteFile(chat, []byte(quoted), 0o644); err != nil {
		t.Fatal(err)
	}
	if GrokChatHolds(updates, marker) {
		t.Error("a tool output quoting the marker counted")
	}
	if GrokChatHolds(chat, marker) {
		t.Error("a path that is not a grok transcript was read")
	}
}
