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

func writeUsageLog(t *testing.T, lines ...string) string {
	t.Helper()
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usage.Path(dir), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The log's stamps are deja's own, written at recall time, so one in the future
// means the clock moved backwards since — and those events sit above everything
// afterwards, in the surface someone opens to see what their agents were
// served. `deja last` and `doctor` name the same state; the log did not (#2122).
func TestLogSaysWhenAnEventIsStampedAhead(t *testing.T) {
	future := time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339Nano)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	// The log shows what was appended last, first, so the event from next year
	// leads. Its stamp is deja's own: the clock it was written from is not the
	// clock reading it now.
	writeUsageLog(t,
		fmt.Sprintf(`{"t":"%s","kind":"hook","bytes":8181,"sessions":2}`, past),
		fmt.Sprintf(`{"t":"%s","kind":"hook","bytes":5151,"sessions":1}`, future),
	)
	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "log") })
	// The premise: the future event really does lead the listing.
	first := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	if !strings.Contains(first, "5.0 KB") {
		t.Fatalf("the event stamped ahead is not at the top, so this measures nothing: %q", first)
	}
	if !strings.Contains(stderr, "later than this machine's clock") {
		t.Errorf("the log led with an event that has not happened and said nothing: %q", strings.TrimSpace(stderr))
	}
}

// And it says it only when there is one.
func TestLogIsQuietWhenNothingIsAhead(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	writeUsageLog(t, fmt.Sprintf(`{"t":"%s","kind":"hook","bytes":8181,"sessions":2}`, past))
	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "log") })
	if !strings.Contains(out, "8.0 KB") {
		t.Fatalf("the log is empty, so this measures nothing: %q", out)
	}
	if strings.Contains(stderr, "later than this machine's clock") {
		t.Errorf("a log with nothing ahead said something was: %q", strings.TrimSpace(stderr))
	}
}
