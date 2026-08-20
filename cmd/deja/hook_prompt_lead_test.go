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

// Two sessions hold the subject: one talked about it and settled nothing, the
// other concluded something. The block has room for one, and the reader is
// served by the conclusion.
//
// `deja bench prompt` has scored this since the concluded_session arm was
// written, and its probe said in a comment that it applied "the same ordering
// the hook applies, from the same function" — but the hook never called it.
// Removing the call again leaves every other test green.
func TestHookPromptLeadsWithTheSessionThatConcluded(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	recent := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	older := time.Now().Add(-96 * time.Hour).UTC().Format(time.RFC3339)

	// Says the subject over and over and never settles it, so ranking puts it
	// first on sheer weight.
	talk := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		talk = append(talk, `{"type":"assistant","sessionId":"kestreltalk","timestamp":"`+recent+
			`","message":{"role":"assistant","content":"looking at kestrel again, still not touching kestrel"}}`)
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "talk.jsonl"), "kestreltalk", talk)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "done.jsonl"), "kestreldone", []string{
		`{"type":"assistant","sessionId":"kestreldone","timestamp":"` + older +
			`","message":{"role":"assistant","content":"the fix: kestrel retries are capped at four"}}`,
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
	in := strings.NewReader(`{"prompt":"what did we decide about kestrel","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "kestrel") {
		t.Fatalf("nothing was recalled, so the ordering below is untested:\n%q", got)
	}
	fix := strings.Index(got, "capped at four")
	talked := strings.Index(got, "still not touching")
	if fix < 0 {
		t.Fatalf("the session that concluded something was not shown at all:\n%q", got)
	}
	if talked >= 0 && talked < fix {
		t.Errorf("the block opens on the session that settled nothing:\n%q", got)
	}
}
