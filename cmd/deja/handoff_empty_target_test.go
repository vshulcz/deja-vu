package main

import (
	"io"
	"strings"
	"testing"
)

// `--to ""` fell through the `target != ""` gate and printed the paste-only
// handoff, so a scripted `--to "$AGENT"` with the variable unset read exactly
// like a handoff to an agent. `--to "   "` was already refused by name, which
// is what made the empty case an oversight (#1647).
func TestHandoffRefusesAnEmptyTarget(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"--to", ""},
		{"--to", "", "cl000001"},
		{"--to", "  ", "cl000001"},
	} {
		err := runHandoff(dir, args, io.Discard)
		if err == nil {
			t.Errorf("handoff %v was accepted", args)
			continue
		}
		if !strings.Contains(err.Error(), "--to") && !strings.Contains(err.Error(), "hand off to") {
			t.Errorf("handoff %v: %v, want it to name the flag or the target", args, err)
		}
	}
	// The controls: a real target is still accepted by the parser, and no --to
	// at all is still the paste-only path rather than an error.
	if err := runHandoff(dir, []string{"--to", "claude"}, io.Discard); err != nil && strings.Contains(err.Error(), "--to") {
		t.Errorf("a real target was refused: %v", err)
	}
	if err := runHandoff(dir, nil, io.Discard); err != nil && strings.Contains(err.Error(), "--to") {
		t.Errorf("no --to must stay the paste path, got: %v", err)
	}
}
