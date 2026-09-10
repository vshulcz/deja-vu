package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// What an agent pulls for itself is the surface with the clearest intent behind
// it, and it was the one recorded without a receiver: 577 answers on this
// machine — recall, recall_context, blame — and not one that could be paired
// with what the agent did next. Two connections must not share an id, or a
// memory one loop keeps pulling reads as a memory many agents came back to.
func TestAnMCPAnswerNamesTheConnectionItWentTo(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	dir := filepath.Join(t.TempDir(), "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	call := `{"jsonrpc":"2.0","id":"r","method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator"}}}` + "\n"
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := serveMCP(index.DefaultDir(), strings.NewReader(call), &out); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	for _, e := range recallReceivers(t, index.DefaultDir()) {
		if e == "" {
			t.Error("an MCP answer was recorded with no receiver")
			continue
		}
		if !strings.HasPrefix(e, "mcp:") {
			t.Errorf("receiver %q does not say which surface it was", e)
		}
		seen[e] = true
	}
	if len(seen) < 2 {
		t.Errorf("two connections shared %d id(s); each run of an agent is its own receiver", len(seen))
	}
}

func recallReceivers(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(usage.Path(dir))
	if err != nil {
		t.Fatalf("no usage log: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e struct {
			Kind string `json:"kind"`
			Into string `json:"into"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Kind == usage.KindRecall {
			out = append(out, e.Into)
		}
	}
	if len(out) == 0 {
		t.Fatal("no recall was recorded, so this measures nothing")
	}
	return out
}
