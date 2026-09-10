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

// The lead is 301 bytes of a 1439-byte block, a session receives a median of 23
// blocks, and the lead's last sentence repeats the line every block ends with.
// Said once, the other 22 blocks stop carrying 6.6 KB of the same sentences.
func TestTheBlockExplainsItselfOncePerSession(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "leadfix.jsonl"), "leadfix", []string{
		`{"type":"user","sessionId":"leadfix","timestamp":"` + old + `","message":{"role":"user","content":"the quokkabloom exporter drops rows at utc_midnight"}}`,
		`{"type":"assistant","sessionId":"leadfix","timestamp":"` + old + `","message":{"role":"assistant","content":"we moved the quokkabloom exporter off utc_midnight and it stopped dropping rows"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "leadfix2.jsonl"), "leadfix2", []string{
		`{"type":"user","sessionId":"leadfix2","timestamp":"` + old + `","message":{"role":"user","content":"the snorblewidget retry budget was capped at three"}}`,
		`{"type":"assistant","sessionId":"leadfix2","timestamp":"` + old + `","message":{"role":"assistant","content":"we capped the snorblewidget retry budget at three attempts and let the fourth fail"}}`,
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
		in := `{"prompt":"` + prompt + `","session_id":"agent-lead"}`
		var out bytes.Buffer
		if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	first := ask("is the quokkabloom exporter still losing rows overnight")
	if !strings.Contains(first, "deja found sessions whose wording matches this request") {
		t.Fatalf("the first block does not explain itself:\n%s", first)
	}

	second := ask("how many attempts does the snorblewidget budget allow now")
	if strings.Contains(second, "deja found sessions whose wording matches this request") {
		t.Errorf("the second block repeats the whole explanation:\n%s", second)
	}
	if !strings.Contains(second, "matched on wording, not meaning") {
		t.Errorf("the second block dropped the caveat that still applies:\n%s", second)
	}
	// What must survive on every block: the untrusted-data line and the line
	// that asks for the citation.
	for _, want := range []string{"untrusted reference data", "If it helped, say:"} {
		if !strings.Contains(second, want) {
			t.Errorf("the second block lost %q:\n%s", want, second)
		}
	}
	if len(second) >= len(first) {
		t.Errorf("the second block is not smaller: %d then %d bytes", len(first), len(second))
	}
}
