package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedLongSession writes one session with many user turns: the first settles
// the question (and, when also is set, concludes one thing more), the rest are other work, and the last of them concluded
// something that shares words with the first turn's passages. A session that
// ran for days looks like this, and its newest conclusion used to be served
// under every question that reached it.
func seedLongSession(t *testing.T, also string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	line := func(role, text string) string {
		at = at.Add(time.Minute)
		return fmt.Sprintf(`{"type":%q,"message":{"role":%q,"content":%q},"timestamp":%q,"sessionId":"long","cwd":"/proj"}`+"\n",
			role, role, text, at.Format(time.RFC3339))
	}
	var b strings.Builder
	b.WriteString(line("user", "why does flimsyshard retry forever while the queue drains"))
	b.WriteString(line("assistant", "so we decided to cap flimsyshard retries at three and log the fourth, because the queue drains slower than it fills"))
	if also != "" {
		b.WriteString(line("assistant", also))
	}
	for i := range longSessionTurns + 5 {
		b.WriteString(line("user", fmt.Sprintf("next, tidy invoice mailer item %d", i)))
		b.WriteString(line("assistant", fmt.Sprintf("looked at invoice mailer item %d, nothing to change there", i)))
	}
	b.WriteString(line("assistant", "in the end we moved the invoice mailer onto the nightly queue, because the queue drains by morning"))
	if err := os.WriteFile(filepath.Join(store, "long.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A long session's conclusions are read around the passages it was quoted
// for. Read off the whole session, the newest one won: on a real store one
// session's last conclusion sat under 205 of 431 recall answers, almost all of
// them about something else.
func TestALongSessionConcludesAboutThePassageItWasQuotedFor(t *testing.T) {
	dir := seedLongSession(t, "we also decided to send the fourth flimsyshard attempt to the dead-letter log")
	text, _, _, _, err := recallTextResult(dir, "flimsyshard retry", "", 0, 0, 4096-recallFrameOverhead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "what this session concluded:") || !strings.Contains(text, "dead-letter log") {
		t.Errorf("the conclusion of the quoted turn is missing:\n%s", text)
	}
	if strings.Contains(text, "nightly queue") {
		t.Errorf("the session's newest conclusion, about other work, was served:\n%s", text)
	}
}

// And when the quoted turn settled nothing past the answer already shown, a
// long session offers nothing rather than its newest line, which is about
// whatever the session did last.
func TestALongSessionOffersNoOtherWorkInstead(t *testing.T) {
	dir := seedLongSession(t, "")
	text, _, _, _, err := recallTextResult(dir, "flimsyshard retry", "", 0, 0, 4096-recallFrameOverhead)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "nightly queue") {
		t.Errorf("a long session offered its newest conclusion, about other work:\n%s", text)
	}
}
