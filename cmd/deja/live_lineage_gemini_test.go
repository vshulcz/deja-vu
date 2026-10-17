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

// Gemini CLI 0.64 keeps a sub-agent's run in chats/<parent>/<child>.jsonl.
// The parent asking, or a later sub-agent of it, got the parent's own
// sub-agent runs as the top hits of its recall (#5134).
func TestGeminiRecallLeavesOutTheLiveSessionsSubAgents(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "gemini", "tmp", "projb", "chats")
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	write := func(path, id, kind string, d time.Duration, user, reply string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"sessionId":%q,"projectHash":"h","startTime":%q,"lastUpdated":%q,"kind":%q}`, id, at(d), at(d), kind) + "\n" +
			fmt.Sprintf(`{"id":"u-%s","timestamp":%q,"type":"user","content":[{"text":%q}]}`, id, at(d), user) + "\n" +
			fmt.Sprintf(`{"id":"a-%s","timestamp":%q,"type":"gemini","content":%q}`, id, at(d), reply) + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	parent := "f7b7a064-d884-4582-a228-e95a8c1ac4f3"
	write(filepath.Join(chats, "session-2026-08-01T09-00-0a1b2c3d.jsonl"), "0a1b2c3d-0000-4000-8000-000000000000", "main", -60*24*time.Hour,
		"when does the marmot release train cut", "The marmot release train cuts on Thursdays after the freeze.")
	write(filepath.Join(chats, "session-2026-10-17T21-47-f7b7a064.jsonl"), parent, "main", -2*time.Minute,
		"note the deploy script", "Noted.")
	write(filepath.Join(chats, parent, "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6.jsonl"), "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6", "subagent", -time.Minute,
		"Check history for the marmot release train cut day.", "No prior sessions matched the marmot release train.")
	write(filepath.Join(chats, parent, "75e99a97-817a-4f82-992d-a80665ab174a.jsonl"), "75e99a97-817a-4f82-992d-a80665ab174a", "subagent", 0,
		"Check history again for the marmot release train cut day.", "No prior history matched the marmot release train.")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	q := json.RawMessage(`{"query":"marmot release train cut day"}`)

	before, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(before, "24b39ab6") || !strings.Contains(before, "75e99a97") {
		t.Fatalf("the fixture never served the sub-agents, so this proves nothing:\n%s", before)
	}

	markSessionLive(dir, parent)
	after, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, own := range []string{"24b39ab6", "75e99a97"} {
		if strings.Contains(after, own) {
			t.Errorf("a sub-agent of the live session is on its page (%s):\n%s", own, after)
		}
	}
	if !strings.Contains(after, "0a1b2c3d") {
		t.Errorf("the session that settled it is not on the page:\n%s", after)
	}
}
