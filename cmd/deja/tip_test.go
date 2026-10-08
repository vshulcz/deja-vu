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

// One tip at most once a day, the next in rotation each time (#4629).
func TestMaybeTipShowsOneADayInRotation(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "index.db.tip")
	day0 := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

	var seen []string
	for i, at := range []time.Time{
		day0,
		day0.Add(time.Hour),      // same day: nothing
		day0.Add(25 * time.Hour), // next tip
		day0.Add(50 * time.Hour),
		day0.Add(75 * time.Hour),
		day0.Add(100 * time.Hour), // wraps to the first again
	} {
		var b bytes.Buffer
		maybeTip(&b, stamp, at)
		if i == 1 {
			if b.Len() != 0 {
				t.Fatalf("a second tip inside a day: %q", b.String())
			}
			continue
		}
		seen = append(seen, strings.TrimSpace(b.String()))
	}
	want := append(append([]string{}, dailyTips...), dailyTips[0])
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Errorf("tips shown:\n%s\nwant:\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
	}
}

// A stamp that cannot be read as one starts the rotation over rather than
// failing; one that cannot be written shows nothing, or it would show on
// every search.
func TestMaybeTipStampEdgeCases(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, "index.db.tip")
	if err := os.WriteFile(stamp, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	maybeTip(&b, stamp, time.Now())
	if strings.TrimSpace(b.String()) != dailyTips[0] {
		t.Errorf("a corrupt stamp did not start at the first tip: %q", b.String())
	}

	b.Reset()
	maybeTip(&b, filepath.Join(dir, "missing", "deeper", "index.db.tip"), time.Now())
	if b.Len() != 0 {
		t.Errorf("a tip was shown though its stamp could not be written: %q", b.String())
	}
}

// Each tip names a command that exists, spelled as the usage spells it.
func TestDailyTipsNameRealCommands(t *testing.T) {
	usage := usageText()
	for _, tip := range dailyTips {
		cmd := strings.Fields(strings.SplitN(tip, "`", 3)[1])[:2]
		if !strings.Contains(usage, strings.Join(cmd, " ")) {
			t.Errorf("tip names %q, which the usage does not list: %s", strings.Join(cmd, " "), tip)
		}
	}
}

// A search whose output is not a terminal (a pipe, a script, a hook) gets its
// results and no tip, and leaves no stamp that would hold a tip back later.
func TestSearchIntoAPipeShowsNoTip(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claude, "alpha", "one.jsonl"), "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"` + at +
			`","message":{"role":"user","content":"the frobnicator retry budget"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, said := captureBoth(t, "search", "frobnicator")
	if !strings.Contains(out, "frobnicator") {
		t.Fatalf("the search found nothing, so this measures nothing:\n%s\n%s", out, said)
	}
	if strings.Contains(said, "tip:") || strings.Contains(out, "tip:") {
		t.Errorf("a piped search showed a tip:\n%s\n%s", out, said)
	}
	if _, err := os.Stat(index.DefaultDir() + ".tip"); err == nil {
		t.Error("a piped search left a tip stamp")
	}
}
