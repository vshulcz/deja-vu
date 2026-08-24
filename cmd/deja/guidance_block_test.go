package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

const guidanceBlockSample = guidanceStart + "\nsome guidance\n" + guidanceEnd + "\n"

// A file whose markers cannot be paired had a whole second block appended, and
// the uninstall after that cut from the first start to the only end — over the
// user's own text — and deleted the file, because what was left was empty
// (#1705).
func TestGuidanceBlockRefusesUnpairedMarkers(t *testing.T) {
	noEnd := strings.ReplaceAll(guidanceBlockSample, guidanceEnd+"\n", "") + "MY OWN text below\n"
	if _, err := updateGuidanceBlock(noEnd, false); err == nil {
		t.Error("install edited a file with no end marker")
	}
	if _, err := updateGuidanceBlock(noEnd, true); err == nil {
		t.Error("uninstall edited a file with no end marker")
	}
	// A lone end marker is what an inline start looks like to a line scanner,
	// and appending below it costs nobody anything — so it is not refused.
	noStart := strings.ReplaceAll(guidanceBlockSample, guidanceStart+"\n", "") + "MY OWN text below\n"
	got, err := updateGuidanceBlock(noStart, false)
	if err != nil {
		t.Fatalf("a lone end marker was refused: %v", err)
	}
	if !strings.Contains(got, "MY OWN text below") {
		t.Errorf("the user's text was dropped:\n%s", got)
	}
}

// Two complete blocks are repaired to one rather than kept for ever.
func TestGuidanceBlockRepairsADuplicate(t *testing.T) {
	two := guidanceBlockSample + "MY OWN text between\n\n" + guidanceBlockSample
	got, err := updateGuidanceBlock(two, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, guidanceStart); n != 1 {
		t.Errorf("expected one block, found %d:\n%s", n, got)
	}
	if !strings.Contains(got, "MY OWN text between") {
		t.Errorf("the user's text was dropped:\n%s", got)
	}
}

// grok writes GROK.md and AGENTS.md from one install. A refusal on the second
// used to leave the first already rewritten (#1705, found in review).
func TestGuidanceGrokTwinIsCheckedFirst(t *testing.T) {
	hermeticEnv(t)
	dir := sources.GrokHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := guidanceStart + "\nSTALE\n" + guidanceEnd + "\n"
	primary := filepath.Join(dir, "GROK.md")
	if err := os.WriteFile(primary, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	half := guidanceStart + "\nsome guidance\n" + "MY OWN in the twin\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(half), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "grok"); err == nil {
		t.Error("install accepted a twin it could not bound")
	}
	b, err := os.ReadFile(primary)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != stale {
		t.Errorf("the primary file was rewritten while the twin refused:\n%s", b)
	}
}

// The control: text above and below an intact block survives, and one block
// stays one block.
func TestGuidanceBlockKeepsTextAround(t *testing.T) {
	around := "MY OWN above\n\n" + guidanceBlockSample + "MY OWN below\n"
	got, err := updateGuidanceBlock(around, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MY OWN above", "MY OWN below"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was dropped:\n%s", want, got)
		}
	}
	if n := strings.Count(got, guidanceStart); n != 1 {
		t.Errorf("expected one block, found %d:\n%s", n, got)
	}
	// And uninstall takes the block, leaving the user's text.
	got, err = updateGuidanceBlock(got, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, guidanceStart) {
		t.Errorf("uninstall left the block:\n%s", got)
	}
	if !strings.Contains(got, "MY OWN above") || !strings.Contains(got, "MY OWN below") {
		t.Errorf("uninstall took the user's text:\n%s", got)
	}
}
