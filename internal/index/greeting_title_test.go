package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func titleOf(ms ...model.Message) string {
	t, _ := sessionTitleFrom(model.Session{Messages: ms})
	return t
}

func userAt(text string, min int) model.Message {
	return model.Message{Role: "user", Text: text, Time: time.Date(2026, 1, 2, 3, min, 0, 0, time.UTC)}
}

// A session that opens with a greeting was named after it — 17 rows reading
// "hi" on a real 800-session store, with the turn that says what the work was
// one line below (#790).
func TestAGreetingDoesNotNameTheSession(t *testing.T) {
	for _, opener := range []string{"hi", "привет", "say ok", "hello again"} {
		got := titleOf(userAt(opener, 0), userAt("why does the docker build fail on arm64", 1))
		if got != "why does the docker build fail on arm64" {
			t.Errorf("opener %q: session titled %q", opener, got)
		}
	}
}

// A short instruction is not a greeting, and the length rule has to leave it
// alone: three words, or anything long enough to say what it wants.
func TestAShortInstructionKeepsItsTitle(t *testing.T) {
	for _, opener := range []string{"fix the build", "run the tests", "deploy staging"} {
		got := titleOf(userAt(opener, 0), userAt("and then the deploy to staging failed again", 1))
		if got != opener {
			t.Errorf("instruction %q was retitled to %q", opener, got)
		}
	}
}

// With nothing else to go on, the short turn still names the session: a title
// that says little beats no title at all.
func TestAGreetingAloneStillNamesTheSession(t *testing.T) {
	if got := titleOf(userAt("hi", 0)); got != "hi" {
		t.Errorf("a one-turn session lost its title: %q", got)
	}
	if got := titleOf(userAt("hi", 0), userAt("ok", 1)); got != "hi" {
		t.Errorf("two thin turns should keep the first: %q", got)
	}
}

// Ordered by the clock, like every other title path — a store titled locally
// and the same store imported elsewhere have to agree (#769).
func TestTheReplacementTitleFollowsTheClock(t *testing.T) {
	// The earlier turn sits later in the slice, so only the clock can pick it.
	got := titleOf(
		userAt("hi", 0),
		userAt("the second thing I asked", 5),
		userAt("the first thing I asked", 1),
	)
	if got != "the first thing I asked" {
		t.Errorf("the replacement did not follow the clock: %q", got)
	}
	// And the other way round, so neither slice order passes by luck.
	got = titleOf(
		userAt("hi", 0),
		userAt("the earlier thing I asked", 1),
		userAt("the later thing I asked", 5),
	)
	if got != "the earlier thing I asked" {
		t.Errorf("the replacement did not follow the clock: %q", got)
	}
	got = titleOf(
		userAt("hi", 0),
		userAt("the second thing I asked", 5),
		userAt("the first thing I asked", 1),
	)
	if got != "the first thing I asked" {
		t.Errorf("the replacement did not follow the clock: %q", got)
	}
}

// The format version is what makes existing rows re-derive their titles:
// incremental ingest reuses the row it already has, so the rules can change
// and nothing re-reads them (#784, and #790 for this change).
func TestTitleRulesRideOnTheFormatVersion(t *testing.T) {
	if version < 26 {
		t.Errorf("index version is %d — the title rules changed without the bump that re-derives them", version)
	}
}
