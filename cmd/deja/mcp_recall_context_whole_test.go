package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// recall_context is the agent's "full story" tool. The search hit carries only
// the matched messages, so an answer worded nothing like the question — the
// decision itself — never reached the agent until the hit was upgraded to the
// whole session, the gap CLI ctx already closed (#1011).
func TestMCPRecallContextReturnsTheAnswerNotJustTheMatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	root := filepath.Join(t.TempDir(), "claude")
	proj := filepath.Join(root, "-p")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))

	// The question carries the query word; the answer does not.
	lines := `{"type":"user","sessionId":"s1","cwd":"/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the RETRYPOLICYQ decision for the client"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","cwd":"/p","timestamp":"2026-07-20T10:00:05Z","message":{"role":"assistant","content":"cap it at 3 with jitter DECISIONANSWER"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	in := `{"jsonrpc":"2.0","id":"c","method":"tools/call","params":{"name":"recall_context","arguments":{"query":"RETRYPOLICYQ"}}}` + "\n"
	var out bytes.Buffer
	if err := serveMCP(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "RETRYPOLICYQ") {
		t.Fatalf("recall_context dropped the matched question:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "DECISIONANSWER") {
		t.Fatalf("recall_context handed the agent the question without its answer:\n%s", out.String())
	}
}
