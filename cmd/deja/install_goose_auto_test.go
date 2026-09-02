package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hook is the whole of what -auto adds over the plain target, and it was
// written without a word: `deja install goose` followed by `deja install
// goose-auto` printed "unchanged" three times while switching session-start
// recall on, because the result described the config the first target had
// already written.
func TestGooseAutoSaysItWroteTheHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	exe := filepath.Join(home, "bin", "deja")
	if _, err := installTarget("goose", exe, false); err != nil {
		t.Fatal(err)
	}
	res, err := installTarget("goose-auto", exe, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != gooseHookPath() {
		t.Errorf("reported %q, want the hook it just wrote", res.Path)
	}
	if res.Action == "" || res.Action == "unchanged" {
		t.Errorf("action = %q over a file that did not exist", res.Action)
	}
	// And a second run is honestly unchanged, or every session start would
	// report maintenance that did not happen.
	again, err := installTarget("goose-auto", exe, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Errorf("a repeat run reported %q", again.Action)
	}

	// And the other half: the config edited by hand, the hook already right.
	// Reporting the hook blind would say "unchanged" over a config deja just
	// put back.
	config := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("GOOSE_PROVIDER: openai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := installTarget("goose-auto", exe, false)
	if err != nil {
		t.Fatal(err)
	}
	if back.Action == "unchanged" {
		t.Errorf("the config was rewritten and the run reported %q", back.Action)
	}
	if back.Path != config {
		t.Errorf("reported %q, want the config it put back", back.Path)
	}
}

// Every other installer wires the binary its caller names. This one called
// os.Executable() instead, so the hook pointed at whatever process happened to
// run the install — the test binary under test, and the repair's own binary
// when a release moved deja somewhere else.
func TestTheGooseHookWiresTheBinaryItWasGiven(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	const want = "/opt/somewhere/else/deja"
	if _, err := installTarget("goose-auto", want, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(gooseHookPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), want) {
		t.Errorf("the hook does not run the binary it was given:\n%s", b)
	}
	if self, err := os.Executable(); err == nil && strings.Contains(string(b), self) {
		t.Errorf("the hook wired the running process instead:\n%s", b)
	}
}
