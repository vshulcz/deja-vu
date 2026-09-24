package index

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func factSession(id string, msgs ...model.Message) model.Session {
	return model.Session{Harness: "claude", ID: id, Messages: msgs}
}

func at(min int) time.Time {
	return time.Date(2026, 9, 1, 10, min, 0, 0, time.UTC)
}

// What the reader runs after a hit is in the session, and the entry keeps the
// commands nearest the end — a long session's opening moves are usually the
// ones it abandoned.
func TestASessionFactKeepsTheCommandsTheSessionEndedOn(t *testing.T) {
	dir := t.TempDir()
	buildSessionFacts(dir, []model.Session{factSession("s1",
		model.Message{Role: roleCommand, Text: "make test  → exit 2", Time: at(2)},
		model.Message{Role: roleCommand, Text: "SVC_FIXTURES=./fixtures make test\nok  \texample.com/svc/internal/store\t0.6s  → exit 0", Time: at(4)},
	)})
	f, ok := SessionFactOf(dir, "claude", "s1")
	if !ok {
		t.Fatal("the table has no entry for a session that ran commands")
	}
	if len(f.Commands) != 2 {
		t.Fatalf("commands are %v", f.Commands)
	}
	if got := f.Commands[0]; got.Text != "SVC_FIXTURES=./fixtures make test" || !got.Passed() {
		t.Errorf("the command that passed is %#v; want it first and known-good", got)
	}
	if f.Commands[1].Passed() {
		t.Errorf("the failing command reads as passed: %#v", f.Commands[1])
	}
}

// An entry is evidence, so a command with no recorded status must not read as
// one that passed: most of the value is the difference between "was run here"
// and "worked here".
func TestACommandWithNoRecordedStatusDoesNotClaimToHavePassed(t *testing.T) {
	dir := t.TempDir()
	buildSessionFacts(dir, []model.Session{factSession("s2",
		model.Message{Role: roleCommand, Text: "go build ./...", Time: at(1)},
	)})
	f, _ := SessionFactOf(dir, "claude", "s2")
	if len(f.Commands) != 1 {
		t.Fatalf("commands are %v", f.Commands)
	}
	if f.Commands[0].Known || f.Commands[0].Passed() {
		t.Errorf("a command with no exit status reads as %#v", f.Commands[0])
	}
}

// Caps and duplicates: the same command run forty times is one fact.
func TestASessionFactIsCappedAndDeduped(t *testing.T) {
	dir := t.TempDir()
	var msgs []model.Message
	for i := 0; i < 30; i++ {
		msgs = append(msgs, model.Message{Role: roleCommand, Text: "make test  → exit 0", Time: at(i)})
	}
	buildSessionFacts(dir, []model.Session{factSession("s3", msgs...)})
	f, _ := SessionFactOf(dir, "claude", "s3")
	if len(f.Commands) != 1 {
		t.Errorf("the same command appears %d times", len(f.Commands))
	}
}

// A session that ran nothing earns no entry, and an index built
// before the table existed has none at all — both callers read as "nothing to
// add" rather than failing.
func TestNoEntryRatherThanAnEmptyOne(t *testing.T) {
	dir := t.TempDir()
	buildSessionFacts(dir, []model.Session{factSession("s4",
		model.Message{Role: "user", Text: "what did we decide about retries", Time: at(1)},
	)})
	if err := readGob(filepath.Join(dir, sessionFactsFile), new(map[string]SessionFact)); err == nil {
		t.Error("a session with nothing to record still wrote a table")
	}
	if facts := ReadSessionFacts(t.TempDir()); facts != nil {
		t.Errorf("an index with no table read as %v", facts)
	}
	if _, ok := SessionFactOf(t.TempDir(), "claude", "nobody"); ok {
		t.Error("a missing table answered for a session")
	}
}

// A session ends on a commit, a `gh pr checks` and some cleanup, and the
// command that proved the change is further up — so a table of the last few
// commands answers "how is this checked here" almost never. One slot past the
// cap is kept for it.
func TestTheCommandThatCheckedTheWorkSurvivesTheCap(t *testing.T) {
	dir := t.TempDir()
	msgs := []model.Message{
		{Role: roleCommand, Text: "SVC_FIXTURES=./fixtures make test  → exit 0", Time: at(1)},
	}
	for i, c := range []string{"git add -A", "git commit -m wip", "gh pr create", "gh pr checks 1", "pkill -f codex"} {
		msgs = append(msgs, model.Message{Role: roleCommand, Text: c + "  → exit 0", Time: at(2 + i)})
	}
	buildSessionFacts(dir, []model.Session{factSession("s5", msgs...)})
	f, ok := SessionFactOf(dir, "claude", "s5")
	if !ok {
		t.Fatal("no entry for a session that ran commands")
	}
	var found bool
	for _, c := range f.Commands {
		found = found || strings.Contains(c.Text, "make test")
	}
	if !found {
		t.Errorf("the command that checked the work was dropped: %v", f.Commands)
	}
	if len(f.Commands) > sessionFactsCommands+1 {
		t.Errorf("the entry grew past one slot over the cap: %v", f.Commands)
	}
}
