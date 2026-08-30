package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Held back, not thrown away. A pair two sessions agree on is the answer
// whenever there is one; a single sighting is what the store has the rest of
// the time, which on a real machine is most of it.
func TestAConfirmedPairIsNeverDisplacedByASighting(t *testing.T) {
	dir := t.TempDir()
	sig := frictionHash(mustFriction(t, "zsh:1: command not found: timeout"))
	writeFixesForTest(t, dir, []FixPair{
		{Sig: sig, Command: "one session ran this", Candidate: true, Project: "p"},
		{Sig: sig, Command: "two sessions ran this", Project: "p"},
	})

	got := FixesFor(dir, "zsh:1: command not found: timeout", 4, nil)
	if len(got) == 0 {
		t.Fatal("nothing came back")
	}
	for _, p := range got {
		if p.Candidate {
			t.Errorf("a single sighting was served beside a confirmed pair: %+v", p)
		}
	}
}

// With nothing confirmed, the sighting is what there is to say.
func TestASightingIsServedWhenNothingIsConfirmed(t *testing.T) {
	dir := t.TempDir()
	sig := frictionHash(mustFriction(t, "zsh:1: command not found: timeout"))
	writeFixesForTest(t, dir, []FixPair{
		{Sig: sig, Command: "one session ran this", Candidate: true, Project: "p"},
	})

	got := FixesFor(dir, "zsh:1: command not found: timeout", 4, nil)
	if len(got) != 1 || !got[0].Candidate {
		t.Errorf("the store held a sighting and answered with silence: %+v", got)
	}
}

func mustFriction(t *testing.T, line string) string {
	t.Helper()
	l, ok := FrictionLine(line)
	if !ok {
		t.Fatalf("not read as friction: %q", line)
	}
	return l
}

func writeFixesForTest(t *testing.T, dir string, pairs []FixPair) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeGob(fixesPath(dir), pairs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, fixesFile)); err != nil {
		t.Fatal(err)
	}
}
