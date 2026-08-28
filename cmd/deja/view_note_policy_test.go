package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A note promoted from an imported session keeps that project. The session list
// obeys a local-only rule and the Notes tab rendered the note whole — the same
// gap #2315 closed for digests, one tab over (#2317).
func TestViewWithholdsNotesFromHiddenProjects(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peersess", Project: "secretclient/api",
		Role: "assistant", Text: "the quaxbolt overflow was an int32 cast",
		Time: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}
	// One note from the peer's project, one from the machine's own.
	lines := []map[string]any{
		{"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
			"session": "claude:imported-1", "state": "accepted", "project": "imported:secretclient/api",
			"title": "peer decision", "text": "the quaxbolt overflow was an int32 cast"},
		{"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
			"session": "claude:minesess", "state": "accepted", "project": "tmp/mine",
			"title": "my decision", "text": "my own connection pool question"},
	}
	var buf strings.Builder
	for _, l := range lines {
		enc, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(enc)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(notes, []byte(buf.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	if page := readFileString(t, out); !strings.Contains(page, "peer decision") {
		t.Fatalf("the peer's note is not on the page with no policy set, so this measures nothing")
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	if strings.Contains(page, "peer decision") || strings.Contains(page, "secretclient") {
		t.Errorf("the page carries a note from a project the policy withholds")
	}
	if !strings.Contains(page, "my decision") {
		t.Errorf("the page dropped the machine's own note")
	}
}
