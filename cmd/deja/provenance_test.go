package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// "imported:" tells a reader the work happened somewhere else and stops there.
// Where the sending machine named itself, the listing says which one.
func TestDisplayProjectNamesTheMachine(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   model.Session
		want string
	}{
		{"local work is untouched",
			model.Session{Project: "deja-vu"}, "deja-vu"},
		{"an import names its machine",
			model.Session{Project: "imported:deja-vu", From: "mini"}, "mini:deja-vu"},
		{"an import from an older deja keeps the old label",
			model.Session{Project: "imported:deja-vu"}, "imported:deja-vu"},
		// The prefix is what the trust policy, resume and the note lifecycle
		// read. A machine name arriving on a project that never carried the
		// prefix must not invent one.
		{"a local project is not relabelled",
			model.Session{Project: "deja-vu", From: "mini"}, "deja-vu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayProject(tc.in); got != tc.want {
				t.Errorf("displayProject = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLastParsesTheFromFlag(t *testing.T) {
	_, o, _, err := parseLast([]string{"--from", "mini"})
	if err != nil {
		t.Fatal(err)
	}
	if o.From != "mini" {
		t.Errorf("--from was not read: %q", o.From)
	}
	if _, _, _, err := parseLast([]string{"--from"}); err == nil {
		t.Error("a --from with no value was accepted")
	}
}

// The recent-sources path reads this machine's own stores directly, without
// the index. Asking for another machine's work there must hand back nothing
// rather than this machine's sessions wearing someone else's name.
func TestRecentSourcesAreLocalOnly(t *testing.T) {
	ss := []model.Session{{ID: "a", Harness: "claude", Project: "p"}}
	if got := filterRecentSources(ss, search.Options{From: "mini"}); len(got) != 0 {
		t.Errorf("--from mini returned %d local sessions", len(got))
	}
	if got := filterRecentSources(ss, search.Options{From: "local"}); len(got) != 1 {
		t.Errorf("--from local dropped this machine's work: %d", len(got))
	}
	if got := filterRecentSources(ss, search.Options{}); len(got) != 1 {
		t.Errorf("no filter dropped a session: %d", len(got))
	}
}

func TestUsageMentionsTheFromFlag(t *testing.T) {
	if !strings.Contains(usageText(), "--from") {
		t.Error("`deja help` does not mention --from, so nobody will find it")
	}
}
