package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
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
	if !strings.Contains(s, "last worked ") {
		t.Fatalf("reused block lost the date it was last worked:\n%s", s)
	}
}

// Two questions can agree for the 44 columns the screen has room for and still
// be different work. Judging sameness on the cut text deleted the second one
// (#843).
func TestBriefKeepsReusedLineWhenTitlesOnlyShareTheirOpening(t *testing.T) {
	const asked = "why does the docker build keep failing on the arm64 runner"
	const other = "why does the docker build keep failing on the raspberry pi"
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {asked, 40 * 24 * time.Hour},
		"a2": {asked, 6 * 24 * time.Hour},
		"a3": {other, 5 * 24 * time.Hour},
	})
	for i := 0; i < 4; i++ {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"a3"})
	}
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "asked      ") {
		t.Fatalf("no asked line to judge:\n%s", s)
	}
	if !strings.Contains(s, "reused     ") {
		t.Fatalf("a different question was folded away because its first 44 columns matched:\n%s", s)
	}
}

// The asked span ends at the newest asking; the memory agents keep recalling
// is often an older one. Folding the reuse count into the asked line must not
// carry away the date that says which (#843).
func TestBriefKeepsLastWorkedWhenTheReusedMemoryIsOlderThanTheNewestAsking(t *testing.T) {
	const q = "why does the docker build fail on arm64"
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {q, 40 * 24 * time.Hour},
		"a2": {q, 2 * 24 * time.Hour},
	})
	for i := 0; i < 4; i++ {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"a1"})
	}
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "4× re-used recently") {
		t.Fatalf("reuse count missing:\n%s", s)
	}
	want := "last worked " + search.RelativeDate(time.Now().Add(-40*24*time.Hour))
	if !strings.Contains(s, want) {
		t.Fatalf("the recalled memory is 40 days old, not 2 — %q missing:\n%s", want, s)
	}
}

// The other half: when the reused memory is the newest asking, its date is the
// right end of the span already and printing it twice is the repetition #843
// removed.
func TestBriefDropsLastWorkedWhenItRepeatsTheSpan(t *testing.T) {
	dir := seedAskedAndReused(t, "why does the docker build fail on arm64")
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); strings.Contains(s, "last worked") {
		t.Fatalf("last worked repeats the span's right end:\n%s", s)
	}
}

// A week that served recalls on days that are not today carries a number the
// today line never had, so it is not an echo (#842).
func TestBriefKeepsWeekLineForRecallsServedEarlierInTheWeek(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"d1": {"fix the flaky auth test in the login handler", 20 * time.Minute},
	})
	writeUsageEvents(t, dir, 2, 60*time.Hour)
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "this week  1 session · 2 recalls") {
		t.Fatalf("week line dropped two recalls today never served:\n%s", s)
	}
}

// writeUsageEvents backdates recall events, which the usage recorders cannot
// do: they stamp time.Now().
func writeUsageEvents(t *testing.T, dir string, n int, age time.Duration) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		e := usage.Event{Time: time.Now().Add(-age).UTC(), Kind: usage.KindRecall, Bytes: 100, Sessions: 1}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(usage.Path(dir), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
