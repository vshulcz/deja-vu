package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The usage log is appended to by every recall and read by `deja log`, the
// status bar, the first screen and stats. `statusline` was hardened against ten
// shapes of broken stdin; nothing held the log itself to the same standard,
// though it is the file most likely to be caught mid-write.
//
// Each shape keeps one good line, so this asks two things at once: that no
// reader fails, and that the corruption does not take the record beside it.
func TestEveryReaderSurvivesABrokenUsageLog(t *testing.T) {
	// A size no other row carries, so "the good line survived" cannot be
	// satisfied by something else on the screen.
	good := fmt.Sprintf(`{"t":"%s","kind":"hook","bytes":8181,"sessions":2}`,
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	future := fmt.Sprintf(`{"t":"%s","kind":"hook","bytes":100,"sessions":1}`,
		time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339Nano))

	for _, c := range []struct{ name, body string }{
		{"a line torn mid-write", good + "\n" + `{"t":"2026-08-2`},
		{"a run of NULs", good + "\n" + strings.Repeat("\x00", 64) + "\n"},
		{"a stamp in the future", good + "\n" + future + "\n"},
		{"no newline at the end", good},
		{"a line that is not JSON", good + "\nnot json at all\n"},
		{"a line of a megabyte", good + "\n" +
			`{"t":"2026-08-26T00:00:00Z","kind":"hook","digest":"` + strings.Repeat("x", 1<<20) + `"}` + "\n"},
		// Valid JSON and half a record: docs/json-output.md says a line needs
		// both `t` and `kind` to appear at all, "rather than shown with a
		// missing half".
		{"a line with no kind", good + "\n" + `{"t":"2026-08-26T00:00:00Z","bytes":42}` + "\n"},
		{"a line with no stamp", good + "\n" + `{"kind":"hook","bytes":42}` + "\n"},
		// A line from a newer deja, carrying keys this one does not know: the
		// same document says the log keeps what it was given.
		{"a line with unknown keys", good + "\n" +
			`{"t":"2026-08-26T00:00:00Z","kind":"hook","bytes":7,"weather":"rain"}` + "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			dir := index.DefaultDir()
			if err := index.Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(usage.Path(dir), []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"log"}, {"stats"}, {"statusline"}, {"brief"}} {
				out, err := captureRun(t, args...)
				if err != nil {
					t.Errorf("%v failed: %v", args, err)
				}
				if strings.TrimSpace(out) == "" {
					t.Errorf("%v printed nothing", args)
				}
			}
			// The good line is still there: a broken neighbour must not take
			// the record beside it.
			out, err := captureRun(t, "log")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "8.0 KB") && !strings.Contains(out, "8181") {
				t.Errorf("the good line did not survive:\n%s", out)
			}
		})
	}
}

// An empty log is a state, not a corruption, and every reader says so rather
// than failing.
func TestAnEmptyUsageLogReadsAsEmpty(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usage.Path(dir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no usage recorded yet") {
		t.Errorf("an empty log does not read as empty:\n%s", out)
	}
}

// Half a record is not a record. The log keeps what it was given, but a line
// needs both a stamp and a kind to be shown at all — docs/json-output.md says
// so, "rather than shown with a missing half" — and a line from a newer deja
// carrying keys this one does not know is still a line.
func TestTheLogShowsWholeRecordsAndKeepsUnknownKeys(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"t":"2026-08-26T01:00:00Z","kind":"hook","bytes":4242}`,
		`{"t":"2026-08-26T02:00:00Z","bytes":1717}`,
		`{"kind":"hook","bytes":1818}`,
		`{"t":"2026-08-26T03:00:00Z","kind":"hook","bytes":9393,"weather":"rain"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(usage.Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "4.1 KB") {
		t.Errorf("a whole record is missing:\n%s", out)
	}
	if !strings.Contains(out, "9.2 KB") {
		t.Errorf("a record with an unknown key was dropped:\n%s", out)
	}
	for _, half := range []string{"1.7 KB", "1.8 KB"} {
		if strings.Contains(out, half) {
			t.Errorf("a line missing its stamp or its kind was shown (%s):\n%s", half, out)
		}
	}
}

// A usage event stamped later than this machine's clock: `deja log` shows it,
// and the counters leave it out. Pinned rather than argued — it is the same
// shape #2104 named for sessions, and the decision about it is open.
func TestAFutureUsageEventIsShownButNotCounted(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	future := time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339Nano)
	body := fmt.Sprintf(`{"t":"%s","kind":"hook","bytes":5151,"sessions":1}`, future) + "\n"
	if err := os.WriteFile(usage.Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "5.0 KB") {
		t.Errorf("the log hides an event stamped ahead:\n%s", out)
	}
	bar, err := captureRun(t, "statusline")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bar, "5.0 KB") {
		t.Errorf("the bar counted an event that has not happened: %q", bar)
	}
	// The premise: the same event, stamped an hour ago, is counted — otherwise
	// the absence above says nothing about the future stamp.
	// An hour ago, unless an hour ago was yesterday: the bar counts from local
	// midnight, so between midnight and one in the morning this fixture landed
	// outside the window it is meant to be inside.
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	at := now.Add(-time.Hour)
	if at.Before(midnight) {
		at = midnight.Add(time.Second)
	}
	past := at.UTC().Format(time.RFC3339Nano)
	if err := os.WriteFile(usage.Path(dir), []byte(fmt.Sprintf(`{"t":"%s","kind":"hook","bytes":5151,"sessions":1}`, past)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bar, err = captureRun(t, "statusline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bar, "5.0 KB") {
		t.Fatalf("the bar does not report an ordinary event either, so this measures nothing: %q", bar)
	}
}
