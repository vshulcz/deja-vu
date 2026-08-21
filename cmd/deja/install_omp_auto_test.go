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

	// The context event is the only seam that reaches the model before it
	// answers: `input` never fires in print mode and `before_agent_start`
	// carries the prompt but not the context to change.
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
