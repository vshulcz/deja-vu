package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The listing marks a title that is the agent's own words rather than the
// reader's question (#1100). The import path derives titles the same way and
// lost the mark, so a peer's agent-opened session read as something the reader
// had asked.
func TestImportMarksAnAgentTitle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	batch := t.TempDir()
	line := func(id, role, text, when string) string {
		return `{"harness":"claude","session_id":"` + id + `","project":"work/api","role":"` + role + `","text":"` + text + `","time":"` + when + `"}` + "\n"
	}
	body := line("peerA", "assistant", "we rotate the vault keys weekly now", "2026-08-02T10:00:00Z") +
		line("peerB", "user", "how do we rotate the vault keys", "2026-08-03T10:00:00Z")
	if err := os.WriteFile(filepath.Join(batch, "b.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(dir, batch); err != nil || n != 2 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var agentTitled, userTitled int
	for _, m := range metas {
		switch {
		case m.AgentTitle:
			agentTitled++
			if m.Title != "we rotate the vault keys weekly now" {
				t.Errorf("the wrong row is marked: %q", m.Title)
			}
		default:
			userTitled++
		}
	}
	if agentTitled != 1 || userTitled != 1 {
		t.Errorf("agent-titled=%d user-titled=%d, want 1 and 1", agentTitled, userTitled)
	}
}
