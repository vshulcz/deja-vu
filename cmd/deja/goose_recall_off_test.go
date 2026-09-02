package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DEJA_RECALL=off is the documented kill switch, and every hook reads it —
// except this one. So on goose, off left deja writing a block into the
// reader's AGENTS.md, and that file is re-read every turn, so it kept reaching
// the model. Found while building the control arm of an A/B: both arms shipped
// a block, which is what an off arm is supposed to prove cannot happen.
func TestTheGooseRecallHonoursTheKillSwitch(t *testing.T) {
	gooseHomeForTest(t)

	// Something to write, then the switch.
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(gooseHintsPath())
	if err != nil || !strings.Contains(string(before), gooseRecallStart) {
		t.Fatalf("nothing was written to switch off: %v\n%s", err, before)
	}

	t.Setenv("DEJA_RECALL", "off")
	if err := refreshGooseHintsFor(""); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(gooseHintsPath()); err == nil && strings.Contains(string(b), gooseRecallStart) {
		t.Errorf("the block survived the kill switch:\n%s", b)
	}
}

// Off must not take the reader's own lines with it: the switch is about deja's
// block, not about the file.
func TestTheKillSwitchLeavesTheReadersLines(t *testing.T) {
	gooseHomeForTest(t)
	path := gooseHintsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# mine\n\nalways use pgx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEJA_RECALL", "off")
	if err := refreshGooseHintsFor(""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the kill switch deleted the reader's file: %v", err)
	}
	if !strings.Contains(string(b), "always use pgx") {
		t.Errorf("the reader's lines went with the block:\n%s", b)
	}
	if strings.Contains(string(b), gooseRecallStart) {
		t.Errorf("the block survived:\n%s", b)
	}
}

// And with the switch off, deja writes nothing where there was nothing —
// "No matching history yet." is a claim about the store, and off is not that.
func TestTheKillSwitchWritesNothingAtAll(t *testing.T) {
	gooseHomeForTest(t)
	t.Setenv("DEJA_RECALL", "off")
	if err := refreshGooseHintsFor(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gooseHintsPath()); err == nil {
		b, _ := os.ReadFile(gooseHintsPath())
		t.Errorf("the switch created a file anyway:\n%s", b)
	}
}

// Under the wrapper the recall lives in the MOIM file, which is deja's own —
// off has to empty it too, or the last thing deja wrote keeps being read every
// turn with the switch thrown.
func TestTheKillSwitchEmptiesTheMoimFile(t *testing.T) {
	gooseHomeForTest(t)
	moim := filepath.Join(t.TempDir(), "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moim); err != nil {
		t.Fatalf("nothing was written to switch off: %v", err)
	}

	t.Setenv("DEJA_RECALL", "off")
	if err := refreshGooseHintsFor(""); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(moim); err == nil && strings.TrimSpace(string(b)) != "" {
		t.Errorf("the MOIM file still holds a recall with the switch off:\n%s", b)
	}
}
