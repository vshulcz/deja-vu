package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// staleStore is a store holding one answer, so the two states an upgrade can
// leave behind can be put on it in turn.
func staleStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-stale")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"st1","cwd":"/w/s","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the quokkabloom exporter keeps dropping rows"}}` + "\n" +
		`{"type":"assistant","sessionId":"st1","cwd":"/w/s","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":"we capped the quokkabloom retry budget at three attempts"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "st1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: the answer is there while the store is current.
	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"quokkabloom retry budget"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "quokkabloom") {
		t.Fatalf("the fixture does not answer at all:\n%s", got)
	}
	return dir
}

// A version bump forces a rebuild, and during it recall used to send the agent
// away: "deja is rebuilding its index for this version — ask again then."
// Measured on a real store, 8 of the 56 recalls an agent actually made landed in
// that sentence, and an agent does not ask again — it concludes there is no
// history (#1733 says exactly that about the refresh case).
//
// A stale content version is not an unreadable store: three of the last four
// bumps changed only what deja derives from a transcript.
func TestRecallAnswersFromAStaleVersionItCanRead(t *testing.T) {
	dir := staleStore(t)
	if err := index.SetManifestVersionForTest(dir, index.CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	if !indexNeedsRebuild(dir) || !index.ReadableSnapshot(dir) {
		t.Fatalf("premise: needsRebuild=%v readable=%v, want a stale store this build can read",
			indexNeedsRebuild(dir), index.ReadableSnapshot(dir))
	}
	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"quokkabloom retry budget"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "ask again then") {
		t.Errorf("recall sent the agent away over a content version it can read:\n%s", got)
	}
	if !strings.Contains(got, "quokkabloom") {
		t.Errorf("recall answered nothing from a readable snapshot:\n%s", got)
	}
}

// And a layout this build cannot be sure of is still a decline: there is nothing
// to trust. Zero is what a store written before the field existed carries.
func TestRecallDeclinesALayoutItCannotRead(t *testing.T) {
	dir := staleStore(t)
	if err := index.SetManifestFormatForTest(dir, 0); err != nil {
		t.Fatal(err)
	}
	if err := index.SetManifestVersionForTest(dir, index.CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	if !indexNeedsRebuild(dir) || index.ReadableSnapshot(dir) {
		t.Fatalf("premise: needsRebuild=%v readable=%v, want a stale store this build cannot read",
			indexNeedsRebuild(dir), index.ReadableSnapshot(dir))
	}
	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"quokkabloom retry budget"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ask again then") {
		t.Errorf("an unreadable layout was served as an answer:\n%s", got)
	}
}
