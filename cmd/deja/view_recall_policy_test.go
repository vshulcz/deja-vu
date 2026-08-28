package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// A digest served while imported content was allowed keeps the peer's project
// and text. The session list on the page obeys a later local-only rule and the
// Recalls tab embedded the same three things — titles, project names, message
// text — verbatim (#2315).
func TestViewWithholdsRecallsFromHiddenProjects(t *testing.T) {
	hermeticEnv(t)
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
		Role: "assistant", Text: "the quaxbolt overflow was an int32 cast in the client's ledger",
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

	// The digest as it was served while imported content was allowed.
	usage.RecordDigestPolicy(dir, usage.KindRecall,
		"<deja-recall>\n1. [claude] imported:secretclient/api · the quaxbolt overflow was an int32 cast\n",
		1, 400, "local+imported")
	// And one about the machine's own work, which nothing withholds.
	usage.RecordDigestPolicy(dir, usage.KindRecall,
		"<deja-recall>\n1. [claude] tmp/mine · my own connection pool question\n",
		1, 400, "local+imported")

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	if page := readFileString(t, out); !strings.Contains(page, "int32 cast") {
		t.Fatalf("the peer's digest is not on the page with no policy set, so this measures nothing")
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
	if strings.Contains(page, "int32 cast") || strings.Contains(page, "secretclient") {
		t.Errorf("the page embeds a digest from a project the policy withholds")
	}
	// The reader's own recall is still there — the rule hides a project, not
	// the tab.
	if !strings.Contains(page, "my own connection pool question") {
		t.Errorf("the page dropped a digest about the machine's own work")
	}
}
