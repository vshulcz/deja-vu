package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Most installs arrive through a marketplace, so the README is never opened and
// the install tail is the only thing deja ever says to that person. It said
// nothing about where to read more or where to report something (#3030).
func TestTheInstallProofSaysWhereToLook(t *testing.T) {
	tmp := hermeticEnv(t)
	seedProofSessions(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	out := captureStderr(t, func() { printInstallProof(dir) })
	if !strings.Contains(out, "deja already knows this machine:") {
		t.Fatalf("the proof did not print, so nothing was measured:\n%s", out)
	}
	if !strings.Contains(out, "docs: vshulcz.github.io/deja-vu") ||
		!strings.Contains(out, "issues: github.com/vshulcz/deja-vu/issues") {
		t.Fatalf("the proof does not say where the docs and the tracker are:\n%s", out)
	}

	// Once per machine: a pointer that repeats is a pointer nobody reads.
	again := captureStderr(t, func() { printInstallProof(dir) })
	if strings.Contains(again, "docs: vshulcz.github.io") {
		t.Fatalf("the line came back on the second run:\n%s", again)
	}
	if !strings.Contains(again, "deja already knows this machine:") {
		t.Fatalf("the proof itself stopped printing with it:\n%s", again)
	}
	_ = tmp
}

// The first-build notice is the only line a marketplace install sees at all, so
// it carries the same pointer — and the two share the marker, so between them
// it is said once.
func TestTheFirstBuildNoticeCarriesItOnce(t *testing.T) {
	hermeticEnv(t)
	seedProofSessions(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	note := builtNote(dir)
	if note == "" {
		t.Fatal("no built note, so nothing was measured")
	}
	if !strings.Contains(note, "docs: vshulcz.github.io/deja-vu") {
		t.Fatalf("the first-build notice does not say where to look:\n%s", note)
	}
	if out := captureStderr(t, func() { printInstallProof(dir) }); strings.Contains(out, "docs: vshulcz.github.io") {
		t.Fatalf("the install said it again after the notice had:\n%s", out)
	}
}

// seedProofSessions puts enough history in the store for the proof block to
// have something real to print.
func seedProofSessions(t *testing.T) {
	t.Helper()
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"p1","cwd":"/work/app","timestamp":"2026-08-02T10:00:00Z","message":{"role":"user","content":"the retry queue keeps double-acking"}}` + "\n" +
		`{"type":"assistant","sessionId":"p1","cwd":"/work/app","timestamp":"2026-08-02T10:01:00Z","message":{"role":"assistant","content":"one shard fixed it"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "p1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
}
