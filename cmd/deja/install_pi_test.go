package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPiExtensionWritesDiscoverableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installPiExtension("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// pi auto-discovers ~/.pi/agent/extensions/*.ts; anywhere else is inert.
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("extension not at the discovered path: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"before_agent_start", // the only event that can inject a message
		"hook-context",
		"hook-prompt",
		"ctx.ui.notify",
		"ctx.ui.setStatus", // pi keeps a footer line; the build belongs there
		"session_start",    // status has to appear before the first prompt
		`"/bin/deja"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("extension missing %q:\n%s", want, src)
		}
	}
}

func TestInstallPiExtensionRemovesOnUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	if _, err := installPiExtension("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installPiExtension("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("extension survived uninstall: %v", err)
	}
	// Uninstalling twice is not an error.
	if r, err := installPiExtension("/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestPiExtensionQuotesExecutablePath(t *testing.T) {
	// A path with a quote or backslash must not break out of the TS string.
	src := piExtensionTS(`/tmp/we"ird\path/deja`)
	if !strings.Contains(src, `const DEJA = "/tmp/we\"ird\\path/deja";`) {
		t.Fatalf("executable path not escaped:\n%s", src)
	}
}

// The command handler takes its argument as `args`, so the search array cannot
// also be `const args`: pi parses the extension as TypeScript and refuses the
// whole file with "Identifier 'args' has already been declared", which takes
// deja's command — and the agent's startup — down with it (#3089).
func TestPiCommandDoesNotRedeclareItsArgument(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	if !strings.Contains(src, "handler: async (args: string") {
		t.Fatalf("the handler no longer takes args, so this guard is stale:\n%s", src)
	}
	if strings.Contains(src, "const args ") || strings.Contains(src, "const args=") {
		t.Fatalf("the search array shadows the handler's `args` parameter, which pi rejects:\n%s", src)
	}
}

// The fix pair only lands if it goes out on tool_result: measured on pi 0.73.1,
// tool_execution_start and tool_execution_end are observe-only, and what their
// handlers return reaches neither the transcript nor the model.
func TestPiRepairsFailedCommandsOnToolResult(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	for _, want := range []string{
		`pi.on("tool_result"`,
		`"hook-tool-after", "--plain"`,
		"event.isError",         // a command that succeeded needs no repair
		"tool_response: output", // deja matches on what the command printed
		"event.toolCallId",      // one lookup per call, not per re-render
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("no fix pair at the point of action, missing %q:\n%s", want, src)
		}
	}
	// Observe-only events cannot carry it, so nothing may be wired there.
	for _, wrong := range []string{
		`pi.on("tool_execution_start"`,
		`pi.on("tool_execution_end"`,
	} {
		if strings.Contains(src, wrong) {
			t.Fatalf("the repair is wired to %s, whose return value pi drops:\n%s", wrong, src)
		}
	}
}

// The file's history has to go out on the read, because pi has no seam that
// runs before an edit: tool_call can only block a tool or rewrite its
// arguments, and by tool_result the edit is already on disk. The read is the
// step an agent takes first.
func TestPiCarriesFileHistoryOnRead(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	for _, want := range []string{
		`event.toolName === "read"`,
		`"hook-tool", "--plain"`,
		"event.input.path", // pi names it path, not file_path
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("no file history at the read, missing %q:\n%s", want, src)
		}
	}
}

// pi names the event session_compact. "compaction" — the name the docs use for
// the feature — registers a handler that never fires, so the session keeps its
// list of shown blocks and stays quiet about a fix it could repeat.
func TestPiForgetsOnTheCompactionEventPiActuallyEmits(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	if !strings.Contains(src, `pi.on("session_compact"`) {
		t.Fatalf("nothing forgets after compaction:\n%s", src)
	}
	if !strings.Contains(src, "hook-precompact") {
		t.Fatalf("session_compact is wired to something other than the forget hook:\n%s", src)
	}
	if strings.Contains(src, `pi.on("compaction"`) {
		t.Fatalf("wired to an event pi does not emit:\n%s", src)
	}
}

// A search the user typed may rebuild the index; a hook may not. One shared
// timeout made /deja answer "nothing matches" for a query with real hits.
func TestPiCommandOutlivesTheHookTimeout(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	if !strings.Contains(src, "120000") {
		t.Fatalf("the slash command runs on the hook budget:\n%s", src)
	}
	if !strings.Contains(src, "timeout = 10000") {
		t.Fatalf("the hook budget is no longer the default:\n%s", src)
	}
}
