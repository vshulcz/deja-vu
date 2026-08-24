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

// viewSessionsEmbedded pulls the array the page hands to its own script.
func viewSessionsEmbedded(t *testing.T, page string) []map[string]any {
	t.Helper()
	const marker = "const S="
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("the view page no longer embeds a session array; this test reads the wrong thing now")
	}
	var rows []map[string]any
	dec := json.NewDecoder(strings.NewReader(page[i+len(marker):]))
	if err := dec.Decode(&rows); err != nil {
		t.Fatalf("decode embedded sessions: %v", err)
	}
	return rows
}

// The page is titles, project names and message previews written to a file
// meant to be looked at and passed around. Under a local-only rule every other
// surface withheld the imported sessions and this one embedded them whole.
func TestViewHonoursTheTrustPolicy(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The imported prefix only exists on sessions that actually arrived by
	// sync, so the peer's session is brought in the way a peer's session
	// arrives rather than written into the local store by hand.
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peersess", Project: "secretclient/api",
		Role: "user", Text: "connection pool tuning for the client",
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

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	before := len(viewSessionsEmbedded(t, readFileString(t, out)))
	if before < 2 {
		t.Fatalf("both sessions must be in the page with no policy set, got %d", before)
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
	for _, row := range viewSessionsEmbedded(t, page) {
		project, _ := row["project"].(string)
		if strings.HasPrefix(project, "imported:") {
			t.Fatalf("the page embeds %q, which the trust policy withholds from every other surface: %v", project, row)
		}
	}
	if strings.Contains(page, "secretclient") {
		t.Fatalf("a withheld project name is still somewhere in the page")
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
