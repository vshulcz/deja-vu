package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// #2316 recognises a withheld project by its name, learned from the sessions in
// the index. Forget those sessions and the name is unknown, so the digest went
// back onto the page. The record now carries the projects it was built from,
// which the policy can answer for on its own (#2324).
func TestViewWithholdsADigestWhoseProjectIsGone(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A digest served from a peer's project, with no session of that project
	// left in the index — forgotten, or simply never indexed here again.
	usage.RecordServedFrom(dir, usage.KindRecall,
		"<deja-recall>\n1. [claude] a peer project · the quaxbolt overflow was an int32 cast\n",
		1, 400, nil, []string{"imported:secretclient/api"}, "local+imported")
	usage.RecordServedFrom(dir, usage.KindRecall,
		"<deja-recall>\n1. [claude] tmp/mine · my own connection pool question\n",
		1, 400, nil, []string{"tmp/mine"}, "local+imported")

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	if page := readFileString(t, out); !strings.Contains(page, "int32 cast") {
		t.Fatalf("the peer's digest is not on the page with no rule set, so this measures nothing")
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
	if strings.Contains(page, "int32 cast") {
		t.Errorf("a digest from a withheld project is on the page, though no session of it is left to name it")
	}
	if !strings.Contains(page, "my own connection pool question") {
		t.Errorf("the page dropped a digest about the machine's own work")
	}
}
