package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// What a session settled and which session ran a command last are both facts
// the build already has in hand, and the point-of-action hook used to go
// looking for them at the moment of the action: a ranking pass to guess which
// sessions might be about the command, then a whole-session load each to ask
// whether they had run it. 133 ms an action against 21 ms (#3001, #3605).
func TestCommandSettledIsALookup(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	session := func(id, project, conclusion string, when time.Time) model.Session {
		return model.Session{
			ID: id, Harness: "claude", Project: project, Started: when, Updated: when,
			Messages: []model.Message{
				{Role: "user", Text: "should we run the orders migration against production?", Time: when},
				{Role: roleCommand, Text: "make migrate-orders", Time: when.Add(time.Minute)},
				{Role: "assistant", Text: conclusion, Time: when.Add(2 * time.Minute)},
			},
		}
	}
	ss := []model.Session{
		session("old", "work/app", "ran the migration against production and it locked the table", at),
		session("new", "work/app", "no: run the orders migration against the replica first", at.Add(time.Hour)),
		session("other", "work/other", "the other project runs it straight through", at.Add(2*time.Hour)),
	}
	writeStore(t, dir, ss)

	yes := func(string) bool { return true }
	got := CommandSettled(dir, "make migrate-orders", []string{"work/app"}, yes)
	if !strings.Contains(got, "replica first") {
		t.Errorf("the newest session in the project did not settle it: %q", got)
	}
	if strings.Contains(got, "locked the table") {
		t.Errorf("an older session's conclusion won: %q", got)
	}

	// Another project's history is not this project's, which is the whole
	// reason ByProject exists.
	got = CommandSettled(dir, "make migrate-orders", []string{"work/other"}, yes)
	if !strings.Contains(got, "straight through") {
		t.Errorf("the other project's own conclusion is missing: %q", got)
	}

	// The trust policy is asked per project, the way every other caller of this
	// table asks it: a withheld project is skipped, not an empty answer.
	no := func(project string) bool { return project != "work/app" }
	got = CommandSettled(dir, "make migrate-orders", []string{"work/app", "work/other"}, no)
	if !strings.Contains(got, "straight through") {
		t.Errorf("a withheld first project emptied the answer instead of being skipped: %q", got)
	}

	// A command nobody ran, and a project with no history, both say nothing.
	if got := CommandSettled(dir, "make something-else", []string{"work/app"}, yes); got != "" {
		t.Errorf("a command with no history answered: %q", got)
	}
	if got := CommandSettled(dir, "make migrate-orders", []string{"work/nowhere"}, yes); got != "" {
		t.Errorf("a project with no history answered: %q", got)
	}
}

// A table written before the session key existed has to send its reader back to
// the search rather than answer nothing: an index built by an older deja is the
// ordinary state for the first session after an upgrade.
func TestCommandSettledSaysNothingWithoutTheSessionKey(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	ss := []model.Session{{
		ID: "one", Harness: "claude", Project: "work/app", Started: at, Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "run the build", Time: at},
			{Role: roleCommand, Text: "make build", Time: at},
			{Role: "assistant", Text: "the build needs the vendor directory first", Time: at},
		},
	}, {
		ID: "two", Harness: "claude", Project: "work/app", Started: at, Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "run the build again", Time: at},
			{Role: roleCommand, Text: "make build", Time: at},
			{Role: "assistant", Text: "still needs the vendor directory", Time: at},
		},
	}}
	writeStore(t, dir, ss)

	// The shape an older table has: the split is there, the key is not.
	table := ReadCommands(dir)
	if len(table) == 0 {
		t.Fatal("the fixture produced no command table")
	}
	for i := range table {
		for proj, pu := range table[i].ByProject {
			pu.LastSession = ""
			table[i].ByProject[proj] = pu
		}
	}
	if err := writeGob(filepath.Join(dir, commandsFile), table); err != nil {
		t.Fatal(err)
	}
	if got := CommandSettled(dir, "make build", []string{"work/app"}, func(string) bool { return true }); got != "" {
		t.Errorf("a table with no session key answered anyway: %q", got)
	}
}

// writeStore builds a real index directory from sessions, the way the other
// tests in this package do: writeSessions publishes the manifest and the
// command table together, which is the pairing CommandSettled reads.
func writeStore(t *testing.T, dir string, ss []model.Session) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
}
