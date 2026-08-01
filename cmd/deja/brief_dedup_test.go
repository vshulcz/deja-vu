package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// seedAgedSessions writes one Claude session per entry at the given age and
// builds the index, so a brief can be measured against a store shaped on
// purpose rather than against whatever the developer's own machine holds.
func seedAgedSessions(t *testing.T, sessions map[string]struct {
	title string
	age   time.Duration
}) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-d")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for sid, s := range sessions {
		when := time.Now().Add(-s.age).UTC().Format("2006-01-02T15:04:05Z")
		body := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/d","timestamp":%q,"message":{"role":"user","content":%q}}`,
			sid, when, s.title) + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

type agedSession = struct {
	title string
	age   time.Duration
}

// A store whose only sessions are today's makes "this week" repeat "today"
// verbatim, which on a one-session store put the same number on three of the
// four lines (#842).
func TestBriefDropsWeekLineThatRestatesToday(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"d1": {"fix the flaky auth test in the login handler", 20 * time.Minute},
	})
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "today      1 session") {
		t.Fatalf("brief lost the today line:\n%s", s)
	}
	if strings.Contains(s, "this week") {
		t.Fatalf("week line restates today with weaker numbers:\n%s", s)
	}
}

// The line still has to appear when the week holds sessions today does not,
// or the fix above has simply deleted a fact.
func TestBriefKeepsWeekLineWhenItAddsSessions(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"d1": {"fix the flaky auth test in the login handler", 20 * time.Minute},
		"d2": {"why does the docker build fail on arm64", 3 * 24 * time.Hour},
	})
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "this week  2 sessions") {
		t.Fatalf("week line dropped though the week holds more than today:\n%s", s)
	}
}

// Recalls the week has and today does not are the other reason to keep it.
func TestBriefKeepsWeekLineWhenItAddsRecalls(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"d1": {"fix the flaky auth test in the login handler", 20 * time.Minute},
	})
	usage.RecordDigest(dir, usage.KindDejaVu, "digest", 1, 2048)
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "déjà vu moment") {
		t.Fatalf("week line dropped a déjà vu moment today's line never carried:\n%s", s)
	}
}

// seedAskedAndReused builds a store where the same question was asked in
// sessions far enough apart to earn the "asked" line, and where recalls land
// on that same work — the normal case, since what you repeat is what gets
// recalled.
func seedAskedAndReused(t *testing.T, reusedTitle string) string {
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {"why does the docker build fail on arm64", 40 * 24 * time.Hour},
		"a2": {"why does the docker build fail on arm64", 6 * 24 * time.Hour},
		"a3": {reusedTitle, 5 * 24 * time.Hour},
	})
	for i := 0; i < 4; i++ {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"a3"})
	}
	return dir
}

// #843: the question asked twice and the memory kept being recalled are
// usually the same work, and the screen printed its title once as "asked" and
// again as "reused".
func TestBriefDoesNotPrintTheSameTitleTwice(t *testing.T) {
	dir := seedAskedAndReused(t, "why does the docker build fail on arm64")
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "asked      ") {
		t.Fatalf("no asked line to judge:\n%s", s)
	}
	// Only the two insight labels are judged here: `recent` lists sessions and
	// will legitimately show a repeated question three times.
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if !strings.HasPrefix(ln, "asked ") && !strings.HasPrefix(ln, "reused ") {
			continue
		}
		if strings.Contains(ln, "why does the docker build fail on arm64") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("title carries %d of the insight labels, want 1:\n%s", n, s)
	}
	if !strings.Contains(s, "4× re-used recently") {
		t.Fatalf("dropping the second title also dropped the reuse count:\n%s", s)
	}
	if strings.Contains(s, "reused     ") {
		t.Fatalf("reused block kept its own line for a title already shown:\n%s", s)
	}
}

// Different work under each label is two facts, and both keep their line.
func TestBriefKeepsReusedLineWhenItNamesOtherWork(t *testing.T) {
	dir := seedAskedAndReused(t, "pgbouncer prepared statements blow up")
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "asked      why does the docker build fail on arm64") {
		t.Fatalf("asked line missing:\n%s", s)
	}
	if !strings.Contains(s, "reused     pgbouncer prepared statements blow up") {
		t.Fatalf("reused line collapsed into a different question:\n%s", s)
	}
}
