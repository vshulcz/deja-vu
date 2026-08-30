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

// The page said how much a rule had hidden and never which rule, while the CLI
// names it. It is also the artifact that leaves the machine, so it names the
// rule and not the file the rule lives in (#2354).
func TestThePageNamesTheRuleThatHidThings(t *testing.T) {
	tmp := hermeticEnv(t)
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
		Role: "user", Text: "the client ledger cutover",
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

	policyPath := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	if !strings.Contains(page, "Held back by the trust policy") {
		t.Fatalf("nothing was held back, so this measures nothing:\n%s", pageStatsBlock(page))
	}
	// The activation as well as the rule, the way the CLI note reads: on a
	// machine whose activations differ, "local-only" alone reads as deja's one
	// rule (#2367).
	if !strings.Contains(page, "search: local-only") {
		t.Errorf("the page does not name the rule and the door it governs:\n%s", pageStatsBlock(page))
	}
	// The rule, not the file it lives in: the page is passed around and the
	// path sits under the reader's home directory.
	if strings.Contains(page, policyPath) {
		t.Errorf("the page carries the path of the policy file")
	}
}
