package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hooks plugin is the whole of what goose-auto adds, and uninstall removed
// it without a word: three "unchanged" lines about config.yaml and nothing
// about the directory that had just been deleted. `uninstall codex-auto` says
// "also removed ~/.codex/hooks.json" about the same thing (#3208).
func TestGooseUninstallNamesThePluginItRemoved(t *testing.T) {
	gooseHomeForTest(t)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Dir(filepath.Dir(gooseHookPath()))
	if _, err := os.Stat(plugin); err != nil {
		t.Fatalf("the install wrote no plugin to remove: %v", err)
	}

	r, err := installGooseAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	said := r.Path + " " + r.Action + " " + r.Note
	if !strings.Contains(said, gooseHookPath()) && !strings.Contains(said, shortHome(gooseHookPath())) {
		t.Errorf("uninstall did not name the plugin it deleted: %q", said)
	}
	if !strings.Contains(said, "removed") {
		t.Errorf("uninstall did not say the plugin was removed: %q", said)
	}
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Errorf("the plugin survived: %v", err)
	}

	// A second uninstall has nothing to remove and must not claim it did.
	r, err = installGooseAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path == gooseHookPath() && r.Action == "removed" {
		t.Errorf("a second uninstall reported removing a plugin that was gone: %#v", r)
	}
	if strings.Contains(r.Note, "removed "+shortHome(gooseHookPath())) {
		t.Errorf("a second uninstall carried the removal in its note: %#v", r)
	}
}
