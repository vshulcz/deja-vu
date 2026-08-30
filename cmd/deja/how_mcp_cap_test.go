package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The MCP how tool cut the list at its limit and said nothing, so an agent
// asking how the tests are run here got eight of thirteen with no way to know
// there were more — and no way to ask a follow-up. The CLI has said so since
// #1632; the tool reimplemented the answer instead of sharing it (#1634).
func TestTheMCPHowToolSaysWhenItCutTheList(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 13; i++ {
		id := "0000000" + string(rune('a'+i)) + "-1111-4000-8000-d6e7f8a9b0c1"
		line := `{"type":"assistant","timestamp":"2026-07-0` + string(rune('1'+i%9)) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./... -run Case` + string(rune('a'+i)) + `"}}]}}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"go test","limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(text, "go test"); n < 3 {
		t.Fatalf("expected three entries:\n%s", text)
	}
	if !strings.Contains(text, "3 of 13") {
		t.Errorf("the cut is silent — nothing says how many there were:\n%s", text)
	}

	// The control: an uncut list says nothing extra.
	text, err = callMCPTool(dir, "how", json.RawMessage(`{"what":"go test","limit":20}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, " of 13") {
		t.Errorf("a list that was not cut says it was:\n%s", text)
	}
}
