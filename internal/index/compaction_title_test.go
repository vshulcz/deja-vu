package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A compaction summary is the harness handing a conversation back to itself.
// The preamble that introduced it is stripped as an injected line, so what is
// left starts at the summary — and 23 of the last 800 sessions on a real store
// were named after it, which is what the recall block then quoted back.
func TestACompactionSummaryDoesNotNameASession(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := "This session is being continued from a previous conversation.\\n\\n" +
		"Summary:\\n1. Primary Request and Intent:\\n   The user asked for the retry budget.\\n" +
		"2. Key Technical Concepts:\\n   the quokkabloom fetcher\\n"
	lines := `{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + summary + `"}}
{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":"why does the quokkabloom fetcher time out"}}
{"type":"assistant","sessionId":"s1","cwd":"/work/app","timestamp":"2026-01-02T03:06:05Z","message":{"role":"assistant","content":"the retry budget is capped at four"}}
`
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) == 0 {
		t.Fatal("nothing indexed, so this measures nothing")
	}
	for _, meta := range m.Sessions {
		if strings.HasPrefix(strings.ToLower(meta.Title), "summary:") {
			t.Errorf("a session is named by its compaction summary: %q", meta.Title)
		}
		if !strings.Contains(meta.Title, "quokkabloom") {
			t.Errorf("the question under the summary did not name the session: %q", meta.Title)
		}
	}
}

// And a person's own "Summary:" is theirs.
func TestAPersonsSummaryStillNamesTheirSession(t *testing.T) {
	if compactionSummary("Summary: we moved the retry budget to four and closed the ticket") {
		t.Error("a sentence a person wrote was read as a compaction")
	}
	if !compactionSummary("Summary:\n1. Primary Request and Intent:\n   the retry budget") {
		t.Error("the compaction template was not recognised")
	}
}
