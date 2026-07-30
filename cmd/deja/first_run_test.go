package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The first command a new user runs is `deja`. Before this, with nothing
// indexed, it printed forty lines of usage; these tests pin the two honest
// answers — build and show, or say plainly that nothing was found.

func TestFirstRunBuildsTheIndexAndBriefs(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"claude-first","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry loop drops the last attempt"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if index.HasManifest(dir) {
		t.Fatal("test starts with an index, which is not the case under test")
	}

	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatalf("brief: %v", err)
	}
	if !index.HasManifest(dir) {
		t.Fatal("the first run should have built the index")
	}
	got := out.String()
	if !strings.Contains(got, "deja-vu") || !strings.Contains(got, "1 session") {
		t.Fatalf("want a brief over the freshly built index, got:\n%s", got)
	}
	if strings.Contains(got, "Usage:") {
		t.Fatalf("usage should not be the answer to a bare command:\n%s", got)
	}
}

func TestFirstRunSaysSoWhenThereIsNoHistory(t *testing.T) {
	// Hermetic env with no stores at all: the build finds nothing, and the
	// answer has to name where deja looked rather than dumping command syntax.
	hermeticEnv(t)
	var out bytes.Buffer
	if err := runBrief(index.DefaultDir(), &out); err != nil {
		t.Fatalf("brief: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "no agent history found") {
		t.Fatalf("got:\n%s", got)
	}
	for _, want := range []string{"deja sources", "deja doctor", "deja help"} {
		if !strings.Contains(got, want) {
			t.Errorf("the empty state should point at %q", want)
		}
	}
	if strings.Contains(got, "deja [flags] <query>") {
		t.Fatalf("usage block leaked into the empty state:\n%s", got)
	}
}

func TestBriefLeavesAnExistingIndexAlone(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"claude-x","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"pgbouncer prepared statements"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	var first bytes.Buffer
	if err := runBrief(dir, &first); err != nil {
		t.Fatal(err)
	}
	// A second run must not rebuild: the brief reads the index as it stands.
	before, err := os.Stat(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := runBrief(dir, &second); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("the brief rebuilt an index that already existed")
	}
}

func TestInstallSkipsTheBuildWhenAsked(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"claude-y","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"scripted install"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := runInstall(dir, []string{"claude", "--no-index", "--no-guidance"}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if index.HasManifest(dir) {
		t.Fatal("--no-index should leave the build for later")
	}
}

func TestInstallBuildsTheIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"claude-z","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"index at install time"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := runInstall(dir, []string{"claude", "--no-guidance"}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !index.HasManifest(dir) {
		t.Fatal("install should leave a usable index behind")
	}
}
