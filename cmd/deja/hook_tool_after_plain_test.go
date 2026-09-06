package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// cline's run tool is run_commands. Without it in this set the fix-pair hook
// drops every cline command failure before it looks anything up.
func TestRunCommandsIsACommandTool(t *testing.T) {
	for _, name := range []string{"run_commands", "Bash", "run_terminal_command"} {
		if !isCommandTool(name) {
			t.Errorf("%q is a command tool and was not recognised", name)
		}
	}
	for _, name := range []string{"editor", "read_files", "search_codebase"} {
		if isCommandTool(name) {
			t.Errorf("%q is not a command tool", name)
		}
	}
}

// --plain prints the block itself, not the PostToolUse envelope: a host that
// puts the repair somewhere of its own — cline appends it to the failing tool
// result — cannot read a hook JSON wrapper.
func TestFixPairPlainDropsTheEnvelope(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	payload := postToolPayload("run_commands", "make", "", "panic: sql: database is closed", "plain-1")

	var plain bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &plain, true); err != nil {
		t.Fatal(err)
	}
	got := plain.String()
	if !strings.Contains(got, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("--plain did not deliver the repair:\n%s", got)
	}
	if strings.Contains(got, "hookSpecificOutput") || strings.Contains(got, "PostToolUse") {
		t.Fatalf("--plain still wrapped the block in the hook envelope:\n%s", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "<deja-recall>") {
		t.Fatalf("--plain is not the bare block:\n%s", got)
	}

	// The envelope form still wraps, for the hosts that read it.
	var wrapped bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(
		postToolPayload("run_commands", "make", "", "panic: sql: database is closed", "plain-2"),
	), &wrapped, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wrapped.String(), "hookSpecificOutput") {
		t.Fatalf("the default form dropped the envelope:\n%s", wrapped.String())
	}
}
