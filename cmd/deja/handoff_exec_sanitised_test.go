package main

import (
	"strings"
	"testing"
)

// The printed handoff goes through printSanitized — secrets redacted, bidi and
// zero-width characters stripped. The --exec handoff passed the digest to the
// next agent's command line raw, so the same session left the machine clean
// when pasted and unclean when handed over directly.
//
// The two paths differ in how the text leaves, not in what it is, so they have
// to sanitise alike.
func TestTheExecHandoffSanitisesLikeThePrintedOne(t *testing.T) {
	// A bidi override and a zero-width space, which no index-time pass removes
	// because they are a display concern, and a token shaped like a secret.
	raw := "continue the work‮reversed​ here: ghp_0123456789abcdefghijklmnopqrstuvwxyzAB"

	got := handoffPrompt(raw)
	for _, bad := range []string{"‮", "​"} {
		if strings.Contains(got, bad) {
			t.Errorf("an invisible character survived into the prompt: %q", got)
		}
	}
	if strings.Contains(got, "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB") {
		t.Errorf("a secret-shaped token was handed over verbatim: %q", got)
	}
	if !strings.Contains(got, "continue the work") {
		t.Errorf("the digest itself was lost: %q", got)
	}
}

// The wiring, not just the helper: the argv the exec path builds carries the
// sanitised prompt. Before this the two sat a few lines apart and the exec one
// took the raw digest.
func TestTheExecArgvCarriesTheSanitisedPrompt(t *testing.T) {
	argv, ok := handoffArgv("claude", "work‮reversed here ghp_0123456789abcdefghijklmnopqrstuvwxyzAB")
	if !ok || len(argv) < 2 {
		t.Fatalf("no argv for a known target: %v", argv)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "‮") || strings.Contains(joined, "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB") {
		t.Errorf("the command line carries what the printed handoff would have removed: %q", joined)
	}
}

// And the invariant the exec path already had: a prompt cannot start with a
// dash, or the target reads it as a flag.
func TestTheExecHandoffPromptNeverStartsWithADash(t *testing.T) {
	if got := handoffPrompt("-rf /"); strings.HasPrefix(got, "-") {
		t.Errorf("the prompt starts with a dash: %q", got)
	}
}
