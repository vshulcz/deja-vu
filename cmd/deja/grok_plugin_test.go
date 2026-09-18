package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Grok installs the plugin from a marketplace entry that pins a commit, so an
// installed copy stays where the pin left it and nothing said so: the bundle
// went to 0.2.0 while every installed copy was still 0.1.0 (#1828).

// writeGrokPlugin puts a plugin in the place Grok installs one: a generated
// directory name under installed-plugins, with the manifest inside it.
func writeGrokPlugin(t *testing.T, dirName, name, version string) {
	t.Helper()
	dir := filepath.Join(sources.GrokHome(), "installed-plugins", dirName, ".grok-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]string{"name": name, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The number doctor compares against has to be the number the plugin ships, or
// the report is a guess. This is the reason TestKimiManifestsAgree exists: a
// constant nobody checks drifts silently.
func TestGrokPluginVersionMatchesTheManifest(t *testing.T) {
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(repoFile(t, "extensions/grok/.grok-plugin/plugin.json"), &manifest); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if manifest.Version != grokPluginVersion {
		t.Fatalf("grokPluginVersion is %q, the manifest says %q", grokPluginVersion, manifest.Version)
	}
	// The root is found by this name, so the manifest has to carry it.
	if manifest.Name != "deja" {
		t.Fatalf("the plugin manifest names %q, and the installed copy is found by name", manifest.Name)
	}
}

func TestGrokPluginNoteNamesBothVersionsWhenBehind(t *testing.T) {
	hermeticEnv(t)
	writeGrokPlugin(t, "deja-9f3c1a2", "deja", "0.1.0")

	if !grokPluginInstalled() {
		t.Fatal("a plugin under a generated directory name was not found")
	}
	note := grokPluginNote()
	if !strings.Contains(note, "v0.1.0 installed") || !strings.Contains(note, "v"+grokPluginVersion+" ships") {
		t.Fatalf("note = %q, want both versions named", note)
	}
	if !strings.Contains(note, "reinstall") {
		t.Fatalf("note = %q, want it to say what to do", note)
	}
}

// A working copy installed from a local path may legitimately be ahead, and
// deja cannot tell it from a marketplace copy by the files it can see. Telling
// someone their newer plugin is stale would be wrong; reporting the number is
// not.
func TestGrokPluginNoteClaimsNothingAboutACurrentOrNewerCopy(t *testing.T) {
	for _, version := range []string{grokPluginVersion, "9.9.9"} {
		t.Run(version, func(t *testing.T) {
			hermeticEnv(t)
			writeGrokPlugin(t, "deja-9f3c1a2", "deja", version)
			if note := grokPluginNote(); note != "v"+version {
				t.Fatalf("note = %q, want the bare version", note)
			}
		})
	}
}

// Most machines have no Grok at all, and doctor runs on them.
func TestGrokPluginAbsentIsSilentNotAnError(t *testing.T) {
	hermeticEnv(t)
	if grokPluginInstalled() {
		t.Fatal("no plugin is installed, and one was reported")
	}
	if note := grokPluginNote(); note != "" {
		t.Fatalf("note = %q, want silence", note)
	}
	// Installed-plugins exists but holds somebody else's plugin.
	writeGrokPlugin(t, "other-1a2b3c4", "not-deja", "5.0.0")
	if grokPluginInstalled() {
		t.Fatal("another plugin's directory was read as deja's")
	}
	if note := grokPluginNote(); note != "" {
		t.Fatalf("note = %q, want silence", note)
	}
}

// A manifest deja cannot read is not a plugin it can report on, and it must not
// be a crash either.
func TestGrokPluginSurvivesAnUnreadableManifest(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(sources.GrokHome(), "installed-plugins", "deja-9f3c1a2", ".grok-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if grokPluginInstalled() || grokPluginNote() != "" {
		t.Fatal("a manifest that does not parse was reported as an installed plugin")
	}
}

// The report itself: the row says "plugin" rather than "missing", and carries
// the version note when the copy is behind.
func TestDoctorReportsAStaleGrokPlugin(t *testing.T) {
	hermeticEnv(t)
	writeGrokPlugin(t, "deja-9f3c1a2", "deja", "0.1.0")

	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	var row string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "grok") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatal("no grok row in the auto-recall report")
	}
	if !strings.Contains(row, "plugin") {
		t.Fatalf("grok row = %q, want it to say the plugin carries recall", row)
	}
	if !strings.Contains(row, "v0.1.0 installed") {
		t.Fatalf("grok row = %q, want the stale-version note", row)
	}
}

// A current plugin is not news; what it is doing there is, because nothing else
// on screen says it.
func TestDoctorSaysWhatACurrentGrokPluginIsDoing(t *testing.T) {
	hermeticEnv(t)
	writeGrokPlugin(t, "deja-9f3c1a2", "deja", grokPluginVersion)

	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	var row string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "grok") {
			row = l
			break
		}
	}
	if !strings.Contains(row, "plugin") || !strings.Contains(row, "recalls on every prompt") {
		t.Fatalf("grok row = %q, want the plugin row and its sentence", row)
	}
}
