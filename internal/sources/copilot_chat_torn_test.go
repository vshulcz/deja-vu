package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// VS Code appends to a chat's .jsonl while the reply streams, so a read can
// land on a last line that is only half there. The complete lines are the
// session; the torn tail waits for the next pass (#4229). A bad line with more
// after it is still a broken file.
func TestCopilotChatTornLastLineKeepsTheSession(t *testing.T) {
	const ts, rts = int64(1763727100000), int64(1763727160000)
	snap := `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[]}}`
	push := `{"kind":2,"k":["requests"],"v":[` + copilotChatReq("a", "first question", "A", ts, rts) + `]}`
	torn := `{"kind":2,"k":["requests"],"v":[{"requestId":"b","message":{"text":"half writ`
	dir := t.TempDir()

	p := filepath.Join(dir, "torn.jsonl")
	if err := os.WriteFile(p, []byte(snap+"\n"+push+"\n"+torn), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("a torn last line dropped the session: %v %d", err, len(ss))
	}
	if ss[0].Messages[0].Text != "first question" {
		t.Fatalf("user = %q", ss[0].Messages[0].Text)
	}

	mid := filepath.Join(dir, "mid.jsonl")
	if err := os.WriteFile(mid, []byte(snap+"\n"+torn+"\n"+push+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ss, _ := ParseCopilotChatFile(mid); len(ss) != 0 {
		t.Fatalf("a broken line in the middle was skipped: %d sessions", len(ss))
	}
}
