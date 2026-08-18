package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The order anyone trying a new tool follows: run it once on an empty machine,
// work for a day, run it again. The first run leaves an empty index, and the
// second kept reporting a machine with no history while the sessions were on
// disk and the very next command indexed them (#1313).
func TestBriefSaysHistoryArrivedAfterAnEmptyFirstRun(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	var first bytes.Buffer
	if err := runBrief(dir, &first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "no agent history found yet") {
		t.Fatalf("an empty machine did not get the empty-state screen:\n%s", first.String())
	}

	// A day of work lands in the store deja already knows about.
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"why does the retry queue stall"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/work"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	var second bytes.Buffer
	if err := runBrief(dir, &second); err != nil {
		t.Fatal(err)
	}
	out := second.String()
	if strings.Contains(out, "no agent history found yet") {
		t.Errorf("the screen still reports an empty machine after the store filled up:\n%s", out)
	}
	if !strings.Contains(out, "deja index") {
		t.Errorf("the screen does not say what to run:\n%s", out)
	}
}

// And it must not build, take the lock, or narrate: that rule is why the
// obvious fix was rejected. The store is left untouched by the brief, so the
// session count stays at zero until something else indexes it.
func TestBriefStillIndexesNothingWhenItSaysSo(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"why does the retry queue stall"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/work"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ov, err := index.Overview(dir); err != nil || ov.Sessions != 0 {
		t.Errorf("the brief indexed on its own: %d sessions, err %v", ov.Sessions, err)
	}
	for _, narration := range []string{"incremental index", "indexing sessions into"} {
		if strings.Contains(buf.String(), narration) {
			t.Errorf("indexing narration landed on the brief:\n%s", buf.String())
		}
	}
}

// A machine that genuinely has nothing still gets the original screen — the
// one that explains what deja reads and where to look.
func TestBriefKeepsTheEmptyStateWhenNothingArrived(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no agent history found yet") {
		t.Errorf("a machine with no history was told to index something:\n%s", buf.String())
	}
}

// A machine that excludes every project has files in the stores that will
// never be indexed. Telling that reader to run `deja index` on every screen
// would be a nag they can do nothing about.
func TestBriefDoesNotNagAboutExcludedProjects(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "work")
	dir := index.DefaultDir()
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"why does the retry queue stall"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/work"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	// Index once: the excluded work is read and produces nothing, which is the
	// state the reader is left in.
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "history found, not indexed yet") {
		t.Errorf("the screen asks for an index that would change nothing:\n%s", buf.String())
	}
}
