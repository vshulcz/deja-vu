package main

import (
	"strings"
	"testing"
)

// A one-word query that names a subcommand used to run it. Two of those
// commands start an editor, so a search could launch a whole agent.
func TestBareWrapperNamesSearchInsteadOfLaunching(t *testing.T) {
	for _, name := range []string{"aider", "goose"} {
		cmd, ok := commands[name]
		if !ok {
			t.Fatalf("%s is not a command any more; this guard needs updating", name)
		}
		// With no arguments the wrapper must not reach exec.LookPath at all.
		// Searching an empty index is quiet and harmless, which is the point.
		if err := cmd(t.TempDir(), nil); err != nil && strings.Contains(err.Error(), "not on PATH") {
			t.Fatalf("bare `deja %s` tried to launch the harness: %v", name, err)
		}
	}
}

// The plugins shell out with whatever the user typed, so they need a form
// that can never be mistaken for a command.
func TestPluginsUseTheExplicitSearchForm(t *testing.T) {
	for name, js := range map[string]string{
		"pi":     piExtensionTS("/bin/deja"),
		"cline":  clinePluginJS("/bin/deja"),
		"hermes": hermesPluginPy("/bin/deja"),
	} {
		if !strings.Contains(js, `"search"`) {
			t.Fatalf("%s plugin passes the query as the first argument: a query of \"index\" would rebuild the index", name)
		}
	}
}

func TestSearchCommandNeedsAQuery(t *testing.T) {
	if err := cmdSearch(t.TempDir(), nil); err == nil {
		t.Fatal("empty search accepted")
	}
}
