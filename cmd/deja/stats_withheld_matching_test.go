package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The note says a rule hides matching sessions, and stats counted every hidden
// session in the store — so `deja stats --project alpha` reported the sessions
// hidden in beta as matching ones (#2816, the class #2766 and #2794 fixed for
// how and friction).
func TestStatsCountsTheWithheldSessionsItsFiltersWouldShow(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, p := range []struct{ project, id string }{{"alpha", "aaaa0001"}, {"beta", "bbbb0001"}} {
		dir := filepath.Join(root, "-"+p.project)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","timestamp":"2026-07-10T10:00:00Z","sessionId":"` + p.id +
			`-1111-4000-8000-d6e7f8a9b0c1","cwd":"/` + p.project +
			`","message":{"role":"user","content":"the pool is too small"}}`
		if err := os.WriteFile(filepath.Join(dir, p.id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"local":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	note, err := captureRunStderr(t, "stats", "--project", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "hides 1 matching session") {
		t.Errorf("one hidden session is in alpha; the note counts the store:\n%s", note)
	}

	// And without a filter the number is the store's again.
	note, err = captureRunStderr(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "hides 2 matching sessions") {
		t.Errorf("with no filter both hidden sessions match:\n%s", note)
	}
}
