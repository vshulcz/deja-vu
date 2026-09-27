package main

import (
	"encoding/json"
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
	record := func(source string) string {
		b, err := json.MarshalIndent(map[string]any{"version": 1, "plugins": []map[string]any{
			{"name": "deja", "source": source, "root": "plugins/deja", "manifestKind": "reasonix", "enabled": true},
			{"name": "zeta", "root": "plugins/zeta", "manifestKind": "reasonix", "enabled": true},
		}}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	st := record(filepath.Join(t.TempDir(), "someone", "deja-plugin"))
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), st)
	before := treeOf(t, home)
	if _, err := installReasonix("/bin/deja", true, true); err != nil {
		t.Fatal(err)
	}
	sameTree(t, "a home with a foreign deja record after uninstall", before, treeOf(t, home))
	// deja's own record with its directory gone is still deja's to remove.
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), record(reasonixPluginSourceDir()))
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

// The same path as Windows and Reasonix may spell it. The comparison of the
// text runs on every OS; the resolving half is exercised where it can be.
func TestSamePathSpellingReadsWindowsShapes(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		fold bool
		want bool
	}{
		{`C:\Users\runneradmin\AppData\Roaming\deja\reasonix-plugin`, `C:/Users/runneradmin/AppData/Roaming/deja/reasonix-plugin/`, true, true},
		{`C:\Users\RunnerAdmin\x`, `c:\users\runneradmin\x`, true, true},
		{`C:\Users\RunnerAdmin\x`, `c:\users\runneradmin\x`, false, false},
		{`C:\a\.\b\..\c`, `C:/a/c`, true, true},
		{`C:\a\b`, `C:\a\c`, true, false},
		{"/home/u/.config/deja/reasonix-plugin", "/home/u/.config/deja/reasonix-plugin", false, true},
	} {
		if got := samePathSpelling(tc.a, tc.b, tc.fold); got != tc.want {
			t.Errorf("samePathSpelling(%q, %q, fold=%v) = %v, want %v", tc.a, tc.b, tc.fold, got, tc.want)
		}
	}
	if rxSamePath("", "") {
		t.Error("two empty paths are not a match")
	}
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err == nil && !rxSamePath(link, dir) {
		t.Error("a path and a link to it are the same path")
	}
}

// deja's own package is deja's whatever its build is called: on Windows the
// entry names the build, and a test binary is deja.test.exe.
func TestReasonixPackageIsOursWhateverTheBuildIsCalled(t *testing.T) {
	reasonixTestHome(t, false)
	build := filepath.Join(t.TempDir(), "deja.test.exe")
	files := reasonixPackageFiles(build, true)
	if err := writePackageTree(reasonixPluginSourceDir(), files); err != nil {
		t.Fatal(err)
	}
	if err := writePackageTree(reasonixInstalledRoot(), files); err != nil {
		t.Fatal(err)
	}
	if !reasonixPackageIsOurs(reasonixInstalledManifest()) {
		t.Fatal("deja's own package, named for a build called deja.test.exe, read as someone else's")
	}
	if _, err := installReasonix(build, false, true); err != nil {
		t.Errorf("reinstall over deja's own package: %v", err)
	}
	// Changed by hand to run something else, it is not deja's copy any more.
	writeTestFile(t, reasonixInstalledManifest(), strings.Replace(string(files["reasonix-plugin.json"]), "reasonix-ext", "other-ext", 1))
	if reasonixPackageIsOurs(reasonixInstalledManifest()) {
		t.Error("a package that differs from deja's copy and runs no deja binary read as deja's")
	}
}
