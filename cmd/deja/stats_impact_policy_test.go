package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// creditedStore indexes one session in which an agent said "deja-vu recalled",
// and records enough usage for the impact screen to have something to explain.
func creditedStore(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-client")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"role":"assistant","content":"deja-vu recalled: we hit this retry bug in March — reusing that fix"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/client"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"aaa"})
	return dir
}

// Every surface that shows sessions consults the trust policy; this count did
// not, and it is derived from the text of sessions the reader is told they
// cannot see (#1354).
func TestImpactCreditsHonourTheTrustPolicy(t *testing.T) {
	dir := creditedStore(t)
	var before bytes.Buffer
	if err := runStatsImpact(&before, dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before.String(), "credited aloud     1 of") {
		t.Fatalf("the fixture does not produce a credit to withhold:\n%s", before.String())
	}
	writePolicy(t, `{"activations":{"search":{"local":false}}}`)
	var after bytes.Buffer
	if err := runStatsImpact(&after, dir, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after.String(), "credited aloud     1 of") {
		t.Errorf("a credit counted from a session the policy withholds:\n%s", after.String())
	}
	// The usage-log lines are events on this machine, not content from
	// elsewhere, so they stay.
	if !strings.Contains(after.String(), "recalls served") {
		t.Errorf("the policy emptied lines that come from this machine's own log:\n%s", after.String())
	}
}

// And with no rule in force the number is unchanged.
func TestImpactCreditsSurviveTheDefaultPolicy(t *testing.T) {
	dir := creditedStore(t)
	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "credited aloud     1 of") {
		t.Errorf("the default policy withheld a credit:\n%s", out.String())
	}
}
