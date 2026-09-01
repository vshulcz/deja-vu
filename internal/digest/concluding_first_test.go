package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func assistantSession(texts ...string) model.Session {
	s := model.Session{Harness: "claude", Project: "p", ID: "s1"}
	for _, t := range texts {
		s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: t})
	}
	return s
}

// selectConclusions keeps a message for three different reasons — it carries a
// decision marker, it contains a code fence, or it is the last one — and only
// the first is about concluding anything. The walk is newest-first, so a
// message kept for its code fence took the slot from the one that settled the
// question: 28 of the 62 sessions whose block still missed it (#2243).
func TestACodeFenceDoesNotTakeTheSlotFromTheConclusion(t *testing.T) {
	long := strings.Repeat("filler words that pad the message out. ", 12)
	got := Conclusions(assistantSession(
		"Starting on the pool timeouts now. "+long,
		"The fix was raising pgbouncer default_pool_size to 40. "+long,
		"Here is the config as it stands:\n```ini\ndefault_pool_size = 40\nmax_client_conn = 200\n```\n"+long,
		"Left the notes in the README. "+long,
	), 320, 1)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "raising pgbouncer default_pool_size to 40") {
		t.Errorf("the block quoted something else and the conclusion never fit:\n%s", got[0])
	}
}

// Order inside each group is untouched: among messages that concluded
// something, the last one still wins.
func TestAmongConclusionsTheNewestStillWins(t *testing.T) {
	long := strings.Repeat("filler words that pad the message out. ", 12)
	// A plain message in the mix, or the two groups never get sorted at all
	// and this asserts nothing about their internal order.
	got := Conclusions(assistantSession(
		"The fix was pinning pgx to 5.4.3. "+long,
		"That turned out to be wrong; the fix was raising default_pool_size instead. "+long,
		"Left the notes in the README. "+long,
	), 320, 1)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "raising default_pool_size instead") {
		t.Errorf("an earlier conclusion outranked the one that superseded it:\n%s", got[0])
	}
}

// A session where nothing concludes must be unchanged: the last message is
// still what the block quotes.
func TestWithNoConclusionTheOrderIsLeftAlone(t *testing.T) {
	long := strings.Repeat("filler words that pad the message out. ", 12)
	got := Conclusions(assistantSession(
		"Looking at the pool metrics. "+long,
		"Still reading the replica logs. "+long,
	), 320, 1)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "Still reading the replica logs") {
		t.Errorf("the newest message stopped being the default:\n%s", got[0])
	}
}
