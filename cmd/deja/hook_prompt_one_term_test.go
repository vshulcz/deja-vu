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

// A prompt naming exactly one term of art is the sharpest question deja can be
// asked, and it was the one the hook never answered: "do we need pgbouncer
// here" reduces to a single term and a two-term floor returned before the
// store was touched, while the same question padded with a filler noun fired.
// `deja bench prompt` puts the floor at 10/12 real questions and the
// identifier rule at 11/12, precision 1 and no false fire on any of the 10
// negative controls, on seeds 1, 2, 3 and 7 (#R9).
func TestHookPromptAnswersASingleIdentifier(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "oneterm", []string{
		`{"type":"user","sessionId":"oneterm","timestamp":"` + old + `","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"do we need pgbouncer here"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "transaction mode") {
		t.Errorf("one-identifier prompt recalled nothing:\n%q", out.String())
	}

	// The floor still holds where nothing in the prompt identifies anything:
	// a single ordinary word must not open the store.
	if got := promptTermsWorthAsking([]string{"tests"}); got {
		t.Error("a lone plain word cleared the gate")
	}
	if got := promptTermsWorthAsking(nil); got {
		t.Error("an empty prompt cleared the gate")
	}
}
