package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// A machine with only the prompt hook installed never increments `recalls`
// (recall/context kinds) or `injections` (session starts) — its digests land
// in `dejavu_moments`. The empty check read those two counters, so the report
// said nothing was recorded while `deja log`, the stats card and its own JSON
// all listed the injections (#2303).
func TestImpactCountsAPromptHookOnlyMachine(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	for i := 0; i < 5; i++ {
		usage.RecordDigestInto(dir, usage.KindDejaVu, "<deja-recall>\n  - Session: **a** `1`\n", "agent1", 2, 1160,
			[]string{"panic", "quaxbolt"}, "r0", "r1")
	}

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "no recall activity recorded yet") {
		t.Fatalf("five injections read as nothing recorded:\n%s", got)
	}
	if !strings.Contains(got, "déjà vu moments") {
		t.Errorf("the report does not name what did happen:\n%s", got)
	}
	// The closing line divides by what was served — 0 here before the fix,
	// which reads as "none of 0".
	if strings.Contains(got, "of 0 ") {
		t.Errorf("credited-aloud line counts against nothing:\n%s", got)
	}

	// An untouched machine still says so.
	hermeticEnv(t)
	out.Reset()
	if err := runStatsImpact(&out, index.DefaultDir(), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no recall activity recorded yet") {
		t.Errorf("an empty log lost its explanation:\n%s", out.String())
	}
}
