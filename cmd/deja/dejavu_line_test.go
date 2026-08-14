package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The visible line is rate-limited so it does not fire every prompt. Keyed on
// the index alone, that also silenced sessions which had never shown one:
// four fresh agents on a machine received recall inside twenty minutes and one
// of them said so. Running several agents at once is the case deja is for.
func TestDejaVuLineIsRateLimitedPerSessionNotPerMachine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")

	for _, sid := range []string{"agent-0", "agent-1", "agent-2", "agent-3"} {
		if !dejaVuLineDue(dir, sid) {
			t.Errorf("session %s was silenced by another session's notice", sid)
		}
	}
	// Within one session it still holds its tongue.
	if dejaVuLineDue(dir, "agent-0") {
		t.Error("the same session showed the line twice inside the window")
	}
}

// A host that sends no session id keeps the old machine-wide window: still
// better than a line on every prompt.
func TestDejaVuLineWithoutASessionIDKeepsTheOldWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if !dejaVuLineDue(dir, "") {
		t.Fatal("the first notice was withheld")
	}
	if dejaVuLineDue(dir, "") {
		t.Error("an id-less host showed the line twice inside the window")
	}
}

// The window is what expires, not the session: an agent that comes back after
// twenty minutes has earned the line again.
func TestDejaVuLineReturnsAfterTheWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if !dejaVuLineDue(dir, "agent-0") {
		t.Fatal("the first notice was withheld")
	}
	stale := time.Now().Add(-dejaVuLineWindow - time.Minute).Unix()
	path := dir + ".dejavu"
	if err := os.WriteFile(path, []byte("agent-0 "+strconv.FormatInt(stale, 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !dejaVuLineDue(dir, "agent-0") {
		t.Error("the line never came back after the window passed")
	}
}

// The file holds one line per recent session, so it must not grow without
// bound on a machine that opens many.
func TestDejaVuLineFileStaysBounded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	for i := 0; i < dejaVuLineKeep*3; i++ {
		dejaVuLineDue(dir, "agent-"+strconv.Itoa(i))
	}
	b, err := os.ReadFile(dir + ".dejavu")
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if lines > dejaVuLineKeep {
		t.Errorf("the notice file holds %d entries, above the %d cap", lines, dejaVuLineKeep)
	}
}
