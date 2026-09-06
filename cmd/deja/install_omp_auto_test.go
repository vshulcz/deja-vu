package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallOmpAutoWritesAnExtensionModule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	r, err := installOmpAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".omp", "agent", "extensions", "deja", "index.js")
	if r.Path != want {
		t.Fatalf("wrote %q, want %q — omp discovers modules one directory deep under extensions/", r.Path, want)
	}
	body, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)

	// The context event is where a per-prompt block goes in: it hands over the
	// message list on the way to the provider, and `input` never fires in print
	// mode. The once-per-session digest goes in through before_agent_start,
	// which stores what it returns as a message of its own.
	if !strings.Contains(js, `pi.on("context"`) {
		t.Errorf("the extension listens on no event that can inject:\n%s", js)
	}
	if !strings.Contains(js, "return { messages: out }") {
		t.Errorf("the handler returns nothing, so the injection is dropped:\n%s", js)
	}
	if !strings.Contains(js, `"hook-prompt", "--plain"`) {
		t.Errorf("the extension does not ask deja for a block:\n%s", js)
	}
	// Extensions run in-process with no isolation: a throw here takes the whole
	// session with it, so the call has to be guarded.
	if !strings.Contains(js, "catch") {
		t.Errorf("the deja call is unguarded:\n%s", js)
	}
	// The same prompt reaches the handler once per provider request, so the
	// answer is cached against the prompt itself rather than merely stored.
	if !strings.Contains(js, "prompt !== asked") {
		t.Errorf("no guard on the cached prompt, so deja runs again for every provider request:\n%s", js)
	}
}

// omp's bash tool leaves isError false on a command that exits non-zero and
// reports the exit code in details.exitCode instead. Measured on omp 18.1.12: a
// failing build arrived as tool_result isError=false, details.exitCode=1, so a
// repair gated on isError alone never speaks.
func TestOmpRepairsCommandsThatExitedNonZero(t *testing.T) {
	js := ompExtensionJS("/bin/deja")
	for _, want := range []string{
		`pi.on("tool_result"`,
		`"hook-tool-after", "--plain"`,
		`typeof event.details.exitCode === "number"`,
		"event.toolCallId",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("no fix pair at the point of action, missing %q:\n%s", want, js)
		}
	}
}

// Same seam as pi: nothing runs before an edit whose return the model reads, so
// the file's history goes out on the read that precedes it.
func TestOmpCarriesFileHistoryOnRead(t *testing.T) {
	js := ompExtensionJS("/bin/deja")
	for _, want := range []string{
		`event.toolName === "read"`,
		`"hook-tool", "--plain"`,
		"event.input.path",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("no file history at the read, missing %q:\n%s", want, js)
		}
	}
}

// Compaction drops the blocks this session was shown; the list that keeps them
// from repeating outlives it. omp emits session_compact — the name in its own
// extension types — and a handler on anything else never runs.
func TestOmpForgetsAfterCompaction(t *testing.T) {
	js := ompExtensionJS("/bin/deja")
	if !strings.Contains(js, `pi.on("session_compact"`) {
		t.Fatalf("nothing forgets after compaction:\n%s", js)
	}
	if !strings.Contains(js, "hook-precompact") {
		t.Fatalf("session_compact is wired to something other than the forget hook:\n%s", js)
	}
}

// Without the session digest, omp starts every session empty and only speaks
// when a prompt happens to match. The digest is what makes the first answer
// carry the project's recent history.
func TestOmpInjectsTheSessionDigestOnce(t *testing.T) {
	js := ompExtensionJS("/bin/deja")
	if !strings.Contains(js, `pi.on("before_agent_start"`) || !strings.Contains(js, "hook-context") {
		t.Fatalf("no session digest:\n%s", js)
	}
	if !strings.Contains(js, "if (injected) return;") {
		t.Fatalf("the digest is not held to one per session:\n%s", js)
	}
	if !strings.Contains(js, "customType: \"deja-recall\"") {
		t.Fatalf("the digest is not returned as a message, so omp drops it:\n%s", js)
	}
}

func TestOmpExtensionQuotesTheBinaryPath(t *testing.T) {
	js := ompExtensionJS(`C:\Program Files\deja\deja.exe`)
	if !strings.Contains(js, `"C:\\Program Files\\deja\\deja.exe"`) {
		t.Errorf("a Windows path was not escaped for JavaScript:\n%s", js)
	}
}

func TestUninstallOmpAutoRemovesTheModule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installOmpAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	r, err := installOmpAuto("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "removed" {
		t.Errorf("action = %q, want removed", r.Action)
	}
	if _, err := os.Stat(filepath.Dir(r.Path)); !os.IsNotExist(err) {
		t.Errorf("the extension directory survived uninstall: %v", err)
	}

	// Uninstalling again on a machine that has none must not fail or create one.
	again, err := installOmpAuto("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Errorf("second uninstall action = %q, want unchanged", again.Action)
	}
}
