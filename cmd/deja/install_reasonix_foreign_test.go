package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallReasonixLeavesAForeignDejaPackage(t *testing.T) {
	home := reasonixTestHome(t, false)
	foreign := `{"apiVersion":"reasonix.io/plugin/v2","name":"deja","mcpServers":{"deja":{"type":"stdio","command":"node","args":["deja-notes.js"]}}}`
	writeTestFile(t, filepath.Join(home, "plugins", "deja", "reasonix-plugin.json"), foreign)
	before := treeOf(t, home)
	if _, err := installReasonix("/bin/deja", false, true); err == nil || !strings.Contains(err.Error(), "did not write") {
		t.Fatalf("install over a foreign package = %v, want a refusal", err)
	}
	if res, err := installReasonix("/bin/deja", true, true); err != nil || res.Action != "unchanged" {
		t.Fatalf("uninstall of a foreign package = %+v %v, want unchanged", res, err)
	}
	sameTree(t, "the Reasonix home with someone else's deja plugin", before, treeOf(t, home))
}

// A plugin named deja that someone installed with --link: plugins/deja is a
// link and the record points at its own source. Neither is deja's.
func TestUninstallReasonixLeavesALinkedForeignDeja(t *testing.T) {
	home := reasonixTestHome(t, false)
	src := t.TempDir()
	writeTestFile(t, filepath.Join(src, "reasonix-plugin.json"), `{"apiVersion":"reasonix.io/plugin/v2","name":"deja","mcpServers":{"deja":{"type":"stdio","command":"node","args":["notes.js"]}}}`)
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(home, "plugins", "deja")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), "{\n  \"version\": 1,\n  \"plugins\": [\n    {\n      \"name\": \"deja\",\n      \"source\": \"x\",\n      \"root\": \"/elsewhere\",\n      \"manifestKind\": \"reasonix\",\n      \"enabled\": true\n    }\n  ]\n}\n")
	before := treeOf(t, home)
	if _, err := installReasonix("/bin/deja", true, true); err != nil {
		t.Fatal(err)
	}
	sameTree(t, "a home with a linked foreign deja after uninstall", before, treeOf(t, home))
	if _, err := installReasonix("/bin/deja", false, true); err == nil {
		t.Error("install went ahead over a linked foreign deja")
	}
}

// A record named deja with nothing on disk and a source that is not deja's is
// another package's record, and stays.
func TestUninstallReasonixLeavesAForeignRecordWithoutADirectory(t *testing.T) {
	home := reasonixTestHome(t, true)
	st := "{\n  \"version\": 1,\n  \"plugins\": [\n    {\n      \"name\": \"deja\",\n      \"source\": \"/opt/someone/deja-plugin\",\n      \"root\": \"plugins/deja\",\n      \"manifestKind\": \"reasonix\",\n      \"enabled\": true\n    },\n    {\n      \"name\": \"zeta\",\n      \"root\": \"plugins/zeta\",\n      \"manifestKind\": \"reasonix\",\n      \"enabled\": true\n    }\n  ]\n}\n"
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), st)
	before := treeOf(t, home)
	if _, err := installReasonix("/bin/deja", true, true); err != nil {
		t.Fatal(err)
	}
	sameTree(t, "a home with a foreign deja record after uninstall", before, treeOf(t, home))
	// deja's own record with its directory gone is still deja's to remove.
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), strings.Replace(st, "/opt/someone/deja-plugin", reasonixPluginSourceDir(), 1))
	if _, err := installReasonix("/bin/deja", true, true); err != nil {
		t.Fatal(err)
	}
	if names := reasonixStateEntries(t, home); len(names) != 1 || names[0].Name != "zeta" {
		t.Errorf("deja's own orphaned record was kept: %+v", names)
	}
}

// A link at plugins/deja to a directory holding a deja-like package — someone
// linked a copy of their own — is still theirs: install would write through
// the link into their directory.
func TestInstallReasonixDoesNotWriteThroughALink(t *testing.T) {
	home := reasonixTestHome(t, false)
	src := t.TempDir()
	own := `{"apiVersion":"reasonix.io/plugin/v2","name":"deja","mcpServers":{"deja":{"type":"stdio","command":"/opt/deja/bin/deja","args":["mcp"]}}}`
	writeTestFile(t, filepath.Join(src, "reasonix-plugin.json"), own)
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(home, "plugins", "deja")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	before := treeOf(t, src)
	if _, err := installReasonix("/bin/deja", false, true); err == nil {
		t.Error("install went ahead through a linked plugins/deja")
	}
	sameTree(t, "the linked package's own directory", before, treeOf(t, src))
}
