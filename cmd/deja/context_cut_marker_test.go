package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The MCP context reply marked its own trim but sent the agent to `deja ctx
// <id>` "for the whole one", which cuts at the same 8 KB, and to
// recall_context, a tool the one-tool server does not list (#5392).
func TestTheContextReplyPointsAtAReadThatIsWhole(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "kkkk5392-4444-4000-8000-c3d4e5f6a7b8"
	line := func(role, text string) string {
		return `{"type":"` + role + `","message":{"role":"` + role + `","content":` + quoteJSON(text) +
			`},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	}
	body := line("user", "here is the build log "+strings.Repeat("filler ", 6000)) +
		line("assistant", "the root cause is the stale lockfile")
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"context","q":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "recall_context") || strings.Contains(text, "for the whole one") {
		t.Errorf("the reply names a removed tool or calls a cut read whole:\n%s", text[max(0, len(text)-400):])
	}
	if !strings.Contains(text, "`deja show "+id+"`") {
		t.Errorf("the reply does not say where the rest is:\n%s", text[max(0, len(text)-400):])
	}
	if !strings.Contains(text, "the last turn not among them") && !strings.Contains(text, "stale lockfile") {
		t.Errorf("the final turn is neither shown nor reported missing:\n%s", text[max(0, len(text)-400):])
	}
}

// The changes block stopped at its room and said nothing about the edits it
// left out, so it read as everything the session changed (#5392).
func TestTheChangesBlockCountsWhatDidNotFit(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "cccc5392-1111-4000-8000-d6e7f8a9b0c1"
	var b strings.Builder
	n := 0
	line := func(typ string, content any) {
		n++
		raw := mustMarshal(t, map[string]any{
			"type": typ, "sessionId": id, "cwd": "/proj",
			"timestamp": fmt.Sprintf("2026-07-01T10:%02d:%02dZ", n/60, n%60),
			"message":   map[string]any{"role": typ, "content": content},
		})
		b.Write(raw)
		b.WriteByte('\n')
	}
	line("user", "rename the handler everywhere")
	const files = 12
	for i := range files {
		line("assistant", []any{map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", i), "name": "Edit",
			"input": map[string]string{"file_path": fmt.Sprintf("/proj/pkg/f%02d.go", i),
				"old_string": "oldHandler()", "new_string": "newHandler()\n" + strings.Repeat("// moved\n", 120)}}})
	}
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := index.FindByPrefix(dir, id)
	if err != nil || !ok {
		t.Fatalf("session not found: %v", err)
	}
	blk := changesBlock(s, changesBlockMax)
	shown := strings.Count(blk, "pkg/f")
	if shown == 0 || shown >= files {
		t.Fatalf("the fixture should overflow the block: %d of %d shown", shown, files)
	}
	if want := fmt.Sprintf("(%d more changes not shown)", files-shown); !strings.Contains(blk, want) {
		t.Errorf("the block does not count the %d changes it left out:\n%s", files-shown, blk[max(0, len(blk)-300):])
	}
	if len(blk) > changesBlockMax+1 {
		t.Errorf("the count pushed the block to %d bytes, over its %d", len(blk), changesBlockMax)
	}
}
