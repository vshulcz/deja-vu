package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func longSession() model.Session {
	long := "we moved the scheduler into its own process " +
		strings.Repeat("after weighing the options ", 30) + "and then reverted that."
	return model.Session{
		Harness: "claude", Project: "app", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "what did we settle on for the scheduler?"},
			{Role: "assistant", Text: long},
		},
	}
}

// A block that stops mid-sentence reads as a finished thought, and the end of a
// message is where a session says it reverted something. Both surfaces that hand
// a session on — `deja share` and the handover tail — cut to fit and said
// nothing about it.
func TestTruncatedBlocksSayTheyWereCut(t *testing.T) {
	s := longSession()
	for _, tc := range []struct {
		name string
		out  string
	}{
		{"share", Share(s, 400)},
		{"tail", tailSection(s, 300)},
	} {
		got := strings.TrimSpace(tc.out)
		if !strings.HasSuffix(got, "…") {
			t.Errorf("%s ends on an unmarked cut: %q", tc.name, got[max(0, len(got)-50):])
		}
	}
}

// The marker comes out of the budget rather than being added to it: these blocks
// are sized to fit somewhere.
func TestTruncatedBlocksStayWithinBudget(t *testing.T) {
	s := longSession()
	for _, budget := range []int{120, 300, 400, 900} {
		if n := len(Share(s, budget)); n > budget {
			t.Errorf("share at budget %d produced %d bytes", budget, n)
		}
		if n := len(tailSection(s, budget)); n > budget {
			t.Errorf("tail at budget %d produced %d bytes", budget, n)
		}
	}
}

// A session that fits is left alone — no marker, nothing dropped. Otherwise a
// fix could pass the tests above by marking everything.
func TestBlocksThatFitAreUnmarked(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "app", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "what did we settle on for the scheduler?"},
			{Role: "assistant", Text: "we moved it into its own process."},
		},
	}
	got := Share(s, 4000)
	if strings.Contains(got, "…") {
		t.Errorf("a session that fits was marked as cut: %q", got)
	}
	if !strings.Contains(got, "we moved it into its own process.") {
		t.Errorf("a session that fits lost its content: %q", got)
	}
}

// A marker in the middle of a block is worse than none: it says the passage it
// follows was cut and lets the next one read as continuous. Once a chunk is cut,
// the block ends.
func TestNothingFollowsAMarkedCut(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "app", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "what did we settle on for the scheduler?"},
			{Role: "assistant", Text: "we moved the scheduler out " + strings.Repeat("after weighing the options ", 30) + strings.Repeat(" ", 60) + "and then reverted that."},
			{Role: "assistant", Text: "the retry budget stays where it is for now."},
		},
	}
	// Sweep the budget: whether the cut lands in a run of spaces, mid-word or on
	// a rune boundary changes how much room is left after it.
	for budget := 150; budget <= 900; budget++ {
		for _, tc := range []struct {
			name string
			out  string
		}{
			{"share", Share(s, budget)},
			{"tail", tailSection(s, budget)},
		} {
			i := strings.Index(tc.out, "…")
			if i < 0 {
				continue
			}
			if rest := strings.TrimSpace(tc.out[i+len("…"):]); rest != "" {
				t.Fatalf("%s at budget %d continues after a marked cut with %q", tc.name, budget, rest)
			}
		}
	}
}
