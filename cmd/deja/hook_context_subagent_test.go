package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A subagent run is indexed under its own agent id, and the session-start
// digest picked it like any other session — so a reviewer's process talk
// ("Sending verdict", "Worktrees cleaned up") led the block ahead of the
// session that settled the work (#3368).
func TestTheSessionStartDigestPrefersSessionsSomebodyWorkedIn(t *testing.T) {
	claudeRoot := hookDigestFixture(t, true)
	_ = claudeRoot
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := hookDigest(index.DefaultDir())
	if !strings.Contains(got, "quokkabloom batcher") {
		t.Fatalf("the session that settled the work is missing, so this measures nothing:\n%s", got)
	}
	if strings.Contains(got, "Sending verdict") {
		t.Errorf("the subagent run is quoted in the session-start digest:\n%s", got)
	}
}

// Unless the project has nothing else: a subagent's transcript still beats an
// empty digest.
func TestAProjectOfOnlySubagentRunsStillGetsADigest(t *testing.T) {
	hookDigestFixture(t, false)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := hookDigest(index.DefaultDir()); !strings.Contains(got, "Sending verdict") {
		t.Errorf("a project whose only history is subagent runs got nothing:\n%s", got)
	}
}

// hookDigestFixture writes one subagent run and, when withParent, the session
// that spawned it, then puts the test in that project's directory.
func hookDigestFixture(t *testing.T, withParent bool) string {
	// A project of its own: withStatsStores seeds beta, gamma and the rest,
	// and the fallback case needs a project that holds nothing else.
	project := "beta"
	if !withParent {
		project = "quokkaonly"
	}
	t.Helper()
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	recent := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	older := time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339)
	if withParent {
		writeClaudeFixture(t, filepath.Join(claudeRoot, project, "parent.jsonl"), "parent-1", []string{
			`{"type":"user","sessionId":"parent-1","timestamp":"` + older +
				`","message":{"role":"user","content":"the quokkabloom exporter drops spans over 4 KB"}}`,
			`{"type":"assistant","sessionId":"parent-1","timestamp":"` + older +
				`","message":{"role":"assistant","content":"the fix: the quokkabloom batcher caps the payload at 4 KB, raised to 64 KB"}}`,
		})
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, project, "review.jsonl"), "arev-quokka-review-9f1", []string{
		`{"type":"assistant","sessionId":"parent-1","isSidechain":true,"agentId":"arev-quokka-review-9f1",` +
			`"attributionAgent":"reviewer","timestamp":"` + recent +
			`","message":{"role":"assistant","content":"Reviewing the quokkabloom diff now."}}`,
		`{"type":"assistant","sessionId":"parent-1","isSidechain":true,"agentId":"arev-quokka-review-9f1",` +
			`"attributionAgent":"reviewer","timestamp":"` + recent +
			`","message":{"role":"assistant","content":"Sending verdict."}}`,
	})
	cwd := filepath.Join(t.TempDir(), "tmp", project)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	return claudeRoot
}
