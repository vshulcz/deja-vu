package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// `deja ctx <id>` on a session holding one very long message printed 8 KB that
// ended mid-word, dropped the final turn and said nothing, so an agent took it
// for the whole session (#5392).
func TestACutContextSaysWhatItLeftOut(t *testing.T) {
	long := strings.Repeat("filler ", 50_000) // ~350 KB in one message
	s := model.Session{Harness: "claude", ID: "sess-5392", Project: "app", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "here is the build log\n" + long},
			{Role: "assistant", Text: "the root cause is the stale lockfile"},
		}}
	var b bytes.Buffer
	PrintContext(&b, s, "")
	out := b.String()
	if len(out) > ContextBudget+200 {
		t.Errorf("the digest grew to %d bytes", len(out))
	}
	if !strings.Contains(out, "this turn is cut here") || !strings.Contains(out, "`deja show sess-5392`") {
		t.Errorf("the cut turn is not marked:\n%s", out[len(out)-400:])
	}
	if !strings.Contains(out, "filler …\n[this turn is cut here") {
		t.Errorf("the cut fell mid-word:\n%s", out[len(out)-400:])
	}
	if !strings.Contains(out, "1 of 2 turns shown, the last turn not among them") {
		t.Errorf("the dropped final turn is not reported:\n%s", out[len(out)-400:])
	}

	// The control: a session that fits is printed with no marker at all.
	small := s
	small.Messages = []model.Message{{Role: "user", Text: "why does it fail"}, {Role: "assistant", Text: "the stale lockfile"}}
	b.Reset()
	PrintContext(&b, small, "")
	if strings.Contains(b.String(), "turns shown") || strings.Contains(b.String(), "cut here") {
		t.Errorf("a whole session was marked as cut:\n%s", b.String())
	}
}

// A window that keeps the final turn but leaves earlier ones out says how many
// it showed, and does not call the last one missing.
func TestAWindowThatLeavesOutEarlierTurnsCountsThem(t *testing.T) {
	msgs := make([]model.Message, 0, 41)
	for range 40 {
		msgs = append(msgs, model.Message{Role: "user", Text: strings.Repeat("an ordinary question about nothing much ", 8)})
	}
	msgs = append(msgs, model.Message{Role: "assistant", Text: "the quokkabloom retry is capped at three"})
	s := model.Session{Harness: "codex", ID: "sess-window", Messages: msgs}
	var b bytes.Buffer
	PrintContext(&b, s, "quokkabloom")
	out := b.String()
	if !strings.Contains(out, "quokkabloom retry is capped") {
		t.Fatalf("the matched turn is not in the window:\n%s", out)
	}
	if !strings.Contains(out, " of 41 turns shown — `deja show sess-window` prints every turn]") {
		t.Errorf("the turns left out are not counted:\n%s", out[max(0, len(out)-300):])
	}
	if strings.Contains(out, "not among them") {
		t.Errorf("the final turn was shown and still reported missing")
	}
}
