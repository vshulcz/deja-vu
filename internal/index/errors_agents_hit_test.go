package index

import "testing"

// Counted over 102,735 commands and 2,951 failures in real transcripts: two
// thirds of the failures repeat an error the store has already seen, and 44% of
// those were first seen in a different session. The walls below are the ones
// that dominate that count and that nothing here recognised, so `deja friction`
// listed other errors, `deja fix` returned nothing, and the hook at the failure
// stayed silent through 89 sightings of the same shell mistake.
func TestTheWallsThatDominateTheCount(t *testing.T) {
	walls := []string{
		// zsh answering `[ "$a" == "$b" ]`. Twelve characters once the shell's
		// position marker is stripped, and the length bound dropped it.
		"(eval):1: == not found",
		"zsh:1: === not found",
		// A command that ran out of time. The wording is the harness's, and
		// "connection timed out" never covered it.
		"Command timed out after 10m 0s",
		// The harness refusing the agent's own call.
		"<tool_use_error>InputValidationError: [ { expected: string }]",
		"Error: File has been modified since read, either by the user or by a linter",
		"Error: No such tool available: Write. Write is disabled for this session",
		// The tail of a Python traceback, beyond the four exception names that
		// were listed.
		"json.decoder.JSONDecodeError: Expecting value: line 1 column 1 (char 0)",
		"ValueError: invalid literal for int() with base 10",
		"FileNotFoundError: [Errno 2] No such file",
	}
	for _, w := range walls {
		if _, ok := FrictionLine(w); !ok {
			t.Errorf("not recognised as a wall: %q", w)
		}
	}
}

// The shell marker says an error follows, not that everything after it is one.
func TestTheShellMarkerDoesNotLetJunkThrough(t *testing.T) {
	notWalls := []string{
		// deja quoting itself: its own report comes back as tool output in the
		// next session, and this is exactly the shape that taught it about
		// itself before.
		"zsh:1: command not found: timeout · 2026-01-02",
		// Source that talks about a shell error.
		`zsh:1: echo "command not found: $BIN"`,
		// Too little left to recognise.
		"zsh:1: killed",
		// A colon and a number, but not a shell.
		"config:1: retries not found",
	}
	for _, l := range notWalls {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("read as a wall: %q", l)
		}
	}
}
