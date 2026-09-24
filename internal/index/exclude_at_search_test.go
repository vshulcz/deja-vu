package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The exclude list was applied when a store was read and never when its
// sessions were served, so a store already in the index kept answering. The
// case that matters is the harness the reader is sitting in: its live
// transcript is indexed, and a recall came back with the session that asked
// (#3965).
func TestAnExcludedHarnessIsKeptOutOfTheAnswersToo(t *testing.T) {
	setHome(t, t.TempDir())
	ss := []model.Session{
		{Harness: "claude", ID: "prior", Project: "svc", Updated: time.Now().Add(-time.Hour),
			Messages: []model.Message{{Role: "user", Text: "the golden suite needs SVC_FIXTURES"}}},
		{Harness: "opencode", ID: "live", Project: "svc", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "the golden suite needs SVC_FIXTURES"}}},
	}
	if got := ignoredByPolicy(ss); len(got) != 2 {
		t.Fatalf("with nothing excluded the filter served %d of 2", len(got))
	}
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "opencode")
	got := ignoredByPolicy(ss)
	if len(got) != 1 || got[0].Harness != "claude" {
		var names []string
		for _, s := range got {
			names = append(names, s.Harness+":"+s.ID)
		}
		t.Errorf("the excluded harness is still served: %s", strings.Join(names, " "))
	}
}
