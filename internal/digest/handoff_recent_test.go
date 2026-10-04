package digest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A handoff hands over where the session is, not where it began. Its body was
// Share, which reads conclusions from the top of the session: on 11 of 12 real
// sessions every conclusion a handoff carried came from the first tenth of the
// work, and a handoff of a month-long session described a blocker from four
// weeks before it. The compaction packet reads backwards from the end.
func TestAHandoffCarriesTheLatestConclusionsNotTheFirst(t *testing.T) {
	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	s := model.Session{Harness: "opencode", Project: "app", ID: "ses_recent", Updated: at.Add(30 * 24 * time.Hour)}
	add := func(role, text string, d time.Duration) {
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: at.Add(d)})
	}
	add("user", "the release pipeline fails on the arm64 runner, find out why", 0)
	add("user", "never push to main; always open a PR first", time.Minute)
	add("assistant", "Root cause: the publish step is blocked by the expired npm token.", 2*time.Minute)
	for i := 0; i < 120; i++ {
		add("assistant", fmt.Sprintf("reading workflow step %d of the release job, nothing new there", i), time.Duration(i+3)*time.Hour)
		add("user", "дальше", time.Duration(i+3)*time.Hour+time.Minute)
	}
	add("user", "now cut the arm64 build time below ten minutes", 29*24*time.Hour)
	add(sources.RoleCommand, "go test ./... → exit 0", 29*24*time.Hour+time.Minute)
	add("assistant", "Fixed: the arm64 job now reuses the module cache, build time went from 23 to 8 minutes.", 29*24*time.Hour+2*time.Minute)

	out := Handoff(s, 6*1024)
	if !strings.Contains(out, "23 to 8 minutes") {
		t.Errorf("the session's latest conclusion is missing:\n%s", out)
	}
	if !strings.Contains(out, "release pipeline fails on the arm64 runner") {
		t.Errorf("what the session started with is missing:\n%s", out)
	}
	if !strings.Contains(out, "never push to main") {
		t.Errorf("the standing instruction did not travel:\n%s", out)
	}
	if !strings.Contains(out, "[passed] go test ./...") {
		t.Errorf("the check that passed is not named:\n%s", out)
	}
	if strings.Contains(out, "before compaction") {
		t.Errorf("a handoff described its source as a compaction:\n%s", out)
	}
	if strings.Contains(out, "Repository freshness") {
		t.Errorf("a handoff printed a freshness line nothing measured:\n%s", out)
	}
}
