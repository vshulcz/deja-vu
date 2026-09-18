package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session re-ran what had just failed, in the same shell, and the error went
// away for a reason of its own — so the pair is honest about what happened and
// useless as a remedy: "ran next: X / after this failed: X" tells an agent to
// repeat the command that produced the error. The two strings are not equal,
// which is why nothing caught it: a stored command carries its exit status
// (#3720).
func TestFixPairsDropARemedyThatIsTheFailure(t *testing.T) {
	now := time.Now().UTC()
	const cmd = "rtk git status --short && rtk git diff --stat"
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: roleCommand, Text: "$ " + cmd + "  → exit 128", Time: now},
			{Role: roleToolOutput, Text: "fatal: not a repository (or any of the parent directories)", Time: now.Add(time.Second)},
			{Role: roleCommand, Text: "$ " + cmd, Time: now.Add(2 * time.Second)},
			{Role: roleToolOutput, Text: "M internal/index/fixpair.go", Time: now.Add(3 * time.Second)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{s}, func(m model.Session) string { return m.Harness + ":" + m.ID })

	stored := ReadFixes(dir)
	if len(stored) == 0 {
		t.Skip("nothing mined from the fixture; the miner's own rules changed")
	}
	for _, p := range stored {
		if !strings.Contains(p.Error, "not a repository") {
			continue
		}
		if got := FixesFor(dir, p.Error, 3, nil); len(got) != 0 {
			t.Fatalf("served a remedy that is the failing command: ran %q after %q", got[0].Command, got[0].Failed)
		}
	}

	// The control: the same shape with the command actually corrected is still
	// served. This is the pair `deja fix` exists for.
	s2 := model.Session{
		Harness: "claude", ID: "s2",
		Messages: []model.Message{
			{Role: roleCommand, Text: "$ timeout 90 make check  → exit 127", Time: now},
			{Role: roleToolOutput, Text: "command not found: timeout", Time: now.Add(time.Second)},
			{Role: roleCommand, Text: "$ make check", Time: now.Add(2 * time.Second)},
			{Role: roleToolOutput, Text: "ok", Time: now.Add(3 * time.Second)},
		},
	}
	dir2 := t.TempDir()
	buildFixes(dir2, []model.Session{s2}, func(m model.Session) string { return m.Harness + ":" + m.ID })
	kept := ReadFixes(dir2)
	if len(kept) == 0 {
		t.Fatal("the corrected pair was not mined at all")
	}
	if got := FixesFor(dir2, kept[0].Error, 3, nil); len(got) == 0 {
		t.Fatal("a corrected command is the remedy this surface is for, and it was dropped")
	}
}
