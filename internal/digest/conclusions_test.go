package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestConclusionsSurfacesTheOutcomeNewestFirst(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "the deploy keeps failing with a 502"},
		{Role: "assistant", Text: "Checking the logs now."},
		{Role: "assistant", Text: "Root cause: the health check hit / instead of /healthz, so the balancer killed pods mid-rollout. Fixed by pointing the probe at /healthz."},
		{Role: "assistant", Text: "Deploy passes now, three consecutive green rollouts."},
	}}
	got := Conclusions(s, 800, 3)
	if len(got) == 0 {
		t.Fatal("no conclusions from a session that states one")
	}
	// Newest first: the outcome outranks the first thing tried.
	if !strings.Contains(got[0], "passes now") {
		t.Errorf("expected the outcome first, got %q", got[0])
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "Root cause") {
		t.Errorf("the root-cause line is missing: %v", got)
	}
}

func TestConclusionsHonoursBudgetAndCount(t *testing.T) {
	var ms []model.Message
	for i := 0; i < 10; i++ {
		ms = append(ms, model.Message{Role: "assistant", Text: "The fix was to bump the timeout, and it works now."})
	}
	if got := Conclusions(model.Session{Messages: ms}, 4000, 2); len(got) > 2 {
		t.Errorf("max=2 not honoured: %d lines", len(got))
	}
	total := 0
	for _, c := range Conclusions(model.Session{Messages: ms}, 120, 5) {
		total += len(c)
	}
	if total > 120 {
		t.Errorf("budget=120 exceeded: %d bytes", total)
	}
}

func TestConclusionsEmptyForNothingToConclude(t *testing.T) {
	if got := Conclusions(model.Session{}, 500, 3); len(got) != 0 {
		t.Errorf("empty session produced %v", got)
	}
	userOnly := model.Session{Messages: []model.Message{{Role: "user", Text: "hello there"}}}
	if got := Conclusions(userOnly, 500, 3); len(got) != 0 {
		t.Errorf("a session with no assistant turn produced %v", got)
	}
	if got := Conclusions(model.Session{Messages: []model.Message{{Role: "assistant", Text: "x"}}}, 0, 3); len(got) != 0 {
		t.Errorf("zero budget produced %v", got)
	}
}

func TestFirstSentencesKeepsTheOpeningClaim(t *testing.T) {
	in := "Root cause found. The lock was never released. Everything after this is detail that recall does not pay for."
	got := firstSentences(in, 2)
	if !strings.HasPrefix(got, "Root cause found.") || strings.Contains(got, "does not pay") {
		t.Errorf("firstSentences = %q", got)
	}
	// A version number is not a sentence end.
	if got := firstSentences("Upgraded to v1.2 and the crash stopped.", 1); !strings.Contains(got, "crash stopped") {
		t.Errorf("split on a version number: %q", got)
	}
}
