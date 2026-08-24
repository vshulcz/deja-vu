package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja brief` counts sessions stamped later than this machine's clock, so deja
// knows about them; recall put one at the top of the page with its date as
// plain fact. The listing already carries caveats where they are read — an
// earlier attempt, an abandoned approach — and this belongs with them (#1753).
func TestRecallMarksASessionStampedAheadOfTheClock(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(sid string, at time.Time) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/p","timestamp":"` +
			at.UTC().Format(time.RFC3339) + `","message":{"role":"user","content":"blorptastic work in ` + sid + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write("today", now.Add(-time.Hour))
	write("ahead", now.AddDate(0, 0, 30))

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	text, _, _, _, err := recallTextResult(dir, "blorptastic", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ahead") || !strings.Contains(text, "today") {
		t.Fatalf("the fixture is wrong, both sessions should be on the page:\n%s", text)
	}
	marker := "[stamped later than this machine's clock"
	if !strings.Contains(text, marker) {
		t.Errorf("recall does not mark the session ahead of the clock:\n%s", text)
	}
	// And only that one.
	if n := strings.Count(text, marker); n != 1 {
		t.Errorf("the marker appears %d times, want 1:\n%s", n, text)
	}
}

// One rule, two surfaces: whatever the brief counts as ahead of the clock is
// what recall marks. They disagreed while the marker carried a tolerance of its
// own, and the brief's own test pins that three hours out already counts.
func TestBriefAndRecallAgreeOnWhatIsAheadOfTheClock(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		what  string
		at    time.Time
		ahead bool
	}{
		{"an hour ago", now.Add(-time.Hour), false},
		{"a minute ahead", now.Add(time.Minute), true},
		{"three hours ahead", now.Add(3 * time.Hour), true},
		{"five years ahead", now.AddDate(5, 0, 0), true},
		{"exactly now", now, false},
	} {
		if got := index.StampedAhead(tc.at, now); got != tc.ahead {
			t.Errorf("%s: StampedAhead = %v, want %v", tc.what, got, tc.ahead)
		}
	}
}
