package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A turn that is only the host's envelope — a task notification delivered as
// a user prompt — gets nothing: no block, no "you have been here" line. Seen
// live on 2026-09-08 with the envelope's field names as the identifying terms
// (#3156). The same session, asked a real question, still gets its block.
func TestHookPromptStandsDownOnTheHostsOwnTurn(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "notice.jsonl"), "notice", []string{
		`{"type":"user","sessionId":"notice","timestamp":"` + old + `","message":{"role":"user","content":"<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n</task-notification>"}}`,
		`{"type":"assistant","sessionId":"notice","timestamp":"` + old + `","message":{"role":"assistant","content":"That is the earlier background task-id run; it failed on the task-notification path."}}`,
		`{"type":"user","sessionId":"notice","timestamp":"` + old + `","message":{"role":"user","content":"the exporter_batch job drops rows at utc_midnight"}}`,
		`{"type":"assistant","sessionId":"notice","timestamp":"` + old + `","message":{"role":"assistant","content":"utc_midnight rollover: the exporter_batch cursor was compared in local time."}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	ask := func(prompt string) string {
		var out bytes.Buffer
		in := `{"prompt":` + jsonString(prompt) + `,"session_id":"agent-9"}`
		if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := ask("<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n<summary>Background command failed</summary>\n</task-notification>"); strings.TrimSpace(got) != "" {
		t.Fatalf("the host's own turn got a block:\n%s", got)
	}
	// A person's words with the host's reminder appended still count as the
	// person's words — and the reminder's words are not the query.
	got := ask("exporter_batch dropping rows at utc_midnight again\n<system-reminder>\nCAVEMAN MODE ACTIVE\n</system-reminder>")
	if !strings.Contains(got, "utc_midnight") {
		t.Fatalf("a real question with a reminder appended got nothing:\n%s", got)
	}
	if strings.Contains(got, "CAVEMAN") {
		t.Fatalf("the reminder's words reached the block:\n%s", got)
	}
}
