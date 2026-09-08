package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

const compactSummary = `This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.

Summary:
1. Primary Request and Intent:
   Review PR 1159 strictly, improve what can be improved, test it and measure the effect.
   The measurement has to be reproducible from the repository.

2. Key Technical Concepts:
   - the ranking's gaveUpPenalty
   - the recall bench
`

// A session resumed after a compaction opens on the harness's summary of the
// half it dropped. The summary is dropped as an artifact, so the handoff's
// problem statement became whatever the person typed after the resume —
// "продолжай" — and what the work actually was survived nowhere (#3266).
func TestHandoffCarriesWhatTheCompactionDropped(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		Harness: "claude", Project: "deja-vu", ID: "780c3bd3", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: compactSummary, Time: at},
			{Role: "user", Text: "продолжай", Time: at.Add(time.Minute)},
			{Role: "assistant", Text: "Merged it and started the bug hunt.", Time: at.Add(2 * time.Minute)},
		},
	}
	got := Handoff(s, 4000)
	if !strings.Contains(got, "Review PR 1159 strictly") {
		t.Errorf("the handoff lost the only record of what the session was about:\n%s", got)
	}
	if !strings.Contains(got, "harness's summary") {
		t.Errorf("the summary is not named as the harness's own text:\n%s", got)
	}
	// The rest of the block stays out: it runs to kilobytes and the budget is
	// for what was said.
	if strings.Contains(got, "Key Technical Concepts") {
		t.Errorf("the whole summary went in, not the intent:\n%s", got)
	}
}

// The next numbered heading ends the intent even when no blank line does —
// harnesses write the block both ways.
func TestTheIntentStopsAtTheNextHeading(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	tight := "Summary:\n1. Primary Request and Intent:\n" +
		"   Review PR 1159 strictly and measure the effect.\n" +
		"2. Key Technical Concepts:\n   - the ranking's gaveUpPenalty\n"
	s := model.Session{
		Harness: "claude", Project: "deja-vu", ID: "tight", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: tight, Time: at},
			{Role: "user", Text: "продолжай", Time: at.Add(time.Minute)},
		},
	}
	got := Handoff(s, 4000)
	if !strings.Contains(got, "Review PR 1159 strictly") {
		t.Fatalf("the intent is missing, so this measures nothing:\n%s", got)
	}
	if strings.Contains(got, "gaveUpPenalty") {
		t.Errorf("the intent ran into the next section:\n%s", got)
	}
}

// Only when the summary opens the session. With the person's own turns above
// it, they have already said what the work is.
func TestASummaryUnderTheirOwnTurnsIsNotTheProblemStatement(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		Harness: "claude", Project: "deja-vu", ID: "abc", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "the exporter drops spans over 4 KB, find out why", Time: at},
			{Role: "assistant", Text: "Looking at the batcher.", Time: at.Add(time.Minute)},
			{Role: "user", Text: compactSummary, Time: at.Add(2 * time.Minute)},
		},
	}
	got := Handoff(s, 4000)
	if strings.Contains(got, "harness's summary") {
		t.Errorf("a summary under their own turns was promoted anyway:\n%s", got)
	}
	if !strings.Contains(got, "exporter drops spans") {
		t.Errorf("the person's own problem statement is missing:\n%s", got)
	}
}

// A session that was never compacted reads exactly as before.
func TestAnUncompactedSessionHandsOffUnchanged(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		Harness: "claude", Project: "deja-vu", ID: "abc", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "the exporter drops spans over 4 KB, find out why", Time: at},
			{Role: "assistant", Text: "The batcher caps the payload at 4 KB.", Time: at.Add(time.Minute)},
		},
	}
	if got := Handoff(s, 4000); strings.Contains(got, "Earlier, from the harness") {
		t.Errorf("a section appeared on a session with no compaction:\n%s", got)
	}
}
