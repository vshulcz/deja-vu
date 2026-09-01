package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The relevance tier is reached whenever the exact match misses, which a real
// question does all the time — so heading every such answer "No session is
// about this" says it about sessions that are exactly about it. The line has
// to tell the two apart: a question whose subject this store has never held,
// and a question it holds and did not match word for word (#657).
func TestTheRelevanceLeadSaysWhichKindOfMissItIs(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 40; i++ {
		at := time.Now().Add(-time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("s%02d", i)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"the checkout throttle kept tripping under load on shard ` + fmt.Sprint(i) + `"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":"we raised the throttle window and the checkout queue drained"}}`,
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Every word of this one is in the store; the exact AND still misses.
	held := mcpRecallText(t, "checkout throttle load queue window")
	if strings.Contains(held, "No session is about this") {
		t.Errorf("a question the store holds every word of was headed as unheld:\n%s", head(held))
	}
	if !strings.Contains(held, "ranked by relevance") {
		t.Errorf("the relevance answer did not say what it is:\n%s", head(held))
	}

	// This one names something the store has never held.
	unheld := mcpRecallText(t, "grimwald checkout throttle")
	if !strings.Contains(unheld, "No session is about this") {
		t.Errorf("a question about an unheld subject read as an answer:\n%s", head(unheld))
	}
}

func head(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = lines[:6]
	}
	return strings.Join(lines, "\n")
}

func mcpRecallText(t *testing.T, query string) string {
	t.Helper()
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "recall", "arguments": map[string]any{"query": query, "limit": 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := driveMCP(t, string(req))
	res, ok := resp[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("recall %q returned no result: %#v", query, resp[0])
	}
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("recall %q returned no content: %#v", query, res)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}
