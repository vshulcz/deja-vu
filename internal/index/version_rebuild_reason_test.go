package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// seedVersionStore indexes one transcript and then writes the manifest back
// with an older content version, which is what an upgrade looks like from the
// index's side.
func seedVersionStore(t *testing.T, was int) (dir string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"),
		[]byte(`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"the zorblax pool deadlocked"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = was
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Every other reason for a full pass names itself — damage says it is damage,
// a changed exclude list says so, `--rebuild` was asked for. An upgrade said
// only "indexing sessions", the line a first install prints, while doing the
// longest piece of work deja ever does on a large store (#3500).
func TestAnUpgradeSaysWhyItIsRereadingEverything(t *testing.T) {
	dir := seedVersionStore(t, version-1)
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	out := log.String()
	if !strings.Contains(out, "newer index than the one on disk") {
		t.Errorf("the upgrade re-read every source and said nothing about why:\n%s", out)
	}
	// The store still answers afterwards, which is the point of the re-read.
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Errorf("the re-read left %d sessions, want 1", len(ss))
	}
}

// And an ordinary pass says nothing of the kind: the line is a state, not a
// decoration on every run.
func TestAnOrdinaryPassSaysNothingAboutVersions(t *testing.T) {
	dir := seedVersionStore(t, version)
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "newer index than the one on disk") {
		t.Errorf("a pass with nothing to do announced a version change:\n%s", log.String())
	}
	// Nor does a rebuild the user asked for.
	log.Reset()
	if err := Ensure(dir, "claude", true, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "newer index than the one on disk") {
		t.Errorf("`--rebuild` was explained as a version change:\n%s", log.String())
	}
}
