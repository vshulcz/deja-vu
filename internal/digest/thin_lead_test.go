package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Lines the session-start digest quoted on real session starts, judged by
// hand: none of them tells a later session anything.
func TestALineThatSaysNothingIsNotQuotedAsAConclusion(t *testing.T) {
	for _, filler := range []string{
		"Fixed the same way.",
		"работает",
		"Hi. What do you want to work on?",
		"It printed: `hi`",
		"Waiting for CI.",
		"Let me check what formatters or other tests might also depend on the tab layout:",
		"I'll run `echo hi` and report the output.",
		"Merged. Ledger + notes.",
	} {
		if got := SubstantialConclusions(conclusionSession(filler), 400, 2); len(got) != 0 {
			t.Errorf("%q was quoted as a conclusion: %q", filler, got)
		}
	}
}

// Under a command the agent is about to run, a short outcome is the answer:
// the surfaces that pair a line with a command or a file keep it.
func TestAShortOutcomeStaysWhereTheReaderHasTheContext(t *testing.T) {
	if got := Conclusions(conclusionSession("tests pass"), 200, 1); len(got) == 0 || got[0] != "tests pass" {
		t.Errorf("the command-paired surface lost a short outcome: %q", got)
	}
}

// Short is not the same as empty. A line that names what it is about stays,
// however few words it takes.
func TestAShortLineThatNamesSomethingIsKept(t *testing.T) {
	for _, line := range []string{
		"vendoring fixed it",
		"`toolHookMaxBytes` (480 bytes).",
		"We chose a 32-byte nonce.",
		"Verdict: the fix is sound.",
		"Сейчас Hermes — это «читалка»: 15 кронов, почти все производят тексты.",
	} {
		got := SubstantialConclusions(conclusionSession(line), 400, 2)
		if len(got) == 0 || !strings.Contains(got[0], strings.TrimSuffix(line, ".")) {
			t.Errorf("%q was dropped or cut: %q", line, got)
		}
	}
}

// A stub that opens a longer message is not quoted alone: the content is in
// the sentences after it.
func TestAStubOpeningIsMovedPast(t *testing.T) {
	got := SubstantialConclusions(conclusionSession(
		"Готово. Полный отчёт ниже. Ранжирование по дате теперь насыщается, а не заворачивается: "+
			"`dayBoost` ограничен 0.3, тест на 31 декабря зелёный."), 400, 2)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if strings.HasPrefix(got[0], "Готово.") || !strings.Contains(got[0], "dayBoost") {
		t.Errorf("the quote is the stub, not what followed it: %q", got[0])
	}
}

// The budget cut takes the first sentence that carries something, not merely
// the first one.
func TestTheBudgetCutSkipsAStubOpening(t *testing.T) {
	got := SubstantialConclusions(conclusionSession(
		"Готово. The fix was pinning pgx to 5.4.3 in go.mod so the pool stops leaking. "+
			"Both replicas held under the load test for an hour afterwards."), 90, 1)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "pinning pgx to 5.4.3") {
		t.Errorf("the cut kept the stub: %q", got[0])
	}
}

// A message with nothing in it gives way to an older one that has something,
// rather than leaving the block on a greeting.
func TestAnEmptyNewestMessageGivesWayToAnOlderConclusion(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "why did deploys hang?"},
		{Role: "assistant", Text: "The fix was enforcing one lock order in `scheduler.go`: queue lock before worker lock."},
		{Role: "user", Text: "thanks"},
		{Role: "assistant", Text: "Hi. What do you want to work on?"},
	}}
	got := SubstantialConclusions(s, 400, 2)
	if len(got) == 0 || !strings.Contains(got[0], "scheduler.go") {
		t.Errorf("the older conclusion did not take the slot: %q", got)
	}
	for _, l := range got {
		if strings.Contains(l, "What do you want") {
			t.Errorf("the greeting was quoted: %q", got)
		}
	}
}

// The number that opens a list item is not a sentence. Counted as one, the
// two-sentence lead ended at "1." and the item it introduced was cut off.
func TestAListNumberDoesNotEndTheLead(t *testing.T) {
	got := SubstantialConclusions(conclusionSession(
		"Found 3 production bugs. 1. **High — order is marked DONE before notification**, so a failed delivery is never retried."), 400, 1)
	if len(got) == 0 || !strings.Contains(got[0], "marked DONE before notification") {
		t.Errorf("the list item was cut off at its number: %q", got)
	}
}
