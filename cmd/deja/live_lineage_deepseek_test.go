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

// dsh 0.1.1-rc.2 writes each subagent and subagent_fork run to a log of its
// own beside the parent's, naming the parent in the header. The parent asking
// got its own sub-agent back as the top hit, and a grandchild (dsh delegates
// three deep) named only the child between them.
func TestDeepSeekRecallLeavesOutTheLiveSessionsSubAgents(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)
	ws := filepath.Join(root, "--w-projA--")
	now := time.Now()
	write := func(dir, header string, d time.Duration, user, reply string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(ws, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		at := now.Add(d).UnixMilli()
		body := header + "\n" +
			fmt.Sprintf(`{"type":"user/message","seq":7,"time":%d,"data":{"content":[{"type":"text","text":%q}],"source":{"kind":"user"},"role":"user"}}`, at, user) + "\n" +
			fmt.Sprintf(`{"type":"assistant/message","seq":20,"time":%d,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}}`, at+1, reply) + "\n"
		if err := os.WriteFile(filepath.Join(ws, dir, "session.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := func(id string, d time.Duration, extra string) string {
		return fmt.Sprintf(`{"type":"session","version":0,"id":%q,"createdAt":%d,"cwd":"/w/projA"%s}`, id, now.Add(d).UnixMilli(), extra)
	}
	prior, parent := "0a1b2c3d-0000-4000-8000-000000000000", "c559c777-a703-4cfa-9f2e-f4b9ebf675cc"
	child, grand := "286f51ca-3e5c-437d-8e6b-c9d5cfb5e4d3", "3070d4d7-a7c6-4ef8-b71e-cb1f5f5e28e3"
	write("session-"+prior, head("session-"+prior, -60*24*time.Hour, `,"delegationDepth":0`), -60*24*time.Hour,
		"when does the marmot release train cut", "The marmot release train cuts on Thursdays after the freeze.")
	write("session-"+parent, head("session-"+parent, -3*time.Minute, `,"delegationDepth":0`), -3*time.Minute,
		"note the deploy script", "Noted.")
	write(child, head(child, -2*time.Minute, `,"parentSession":"session-`+parent+`","origin":"subagent","delegationDepth":1`), -2*time.Minute,
		"Check history for the marmot release train cut day.", "No prior sessions matched the marmot release train.")
	write(grand, head(grand, -time.Minute, `,"parentSession":"session-`+child+`","origin":"subagent","delegationDepth":2,"seedLength":4`), -time.Minute,
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
	if !strings.Contains(before, child) || !strings.Contains(before, grand) {
		t.Fatalf("the fixture never served the sub-agents, so this proves nothing:\n%s", before)
	}

	markSessionLive(dir, parent)
	after, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, own := range []string{child, grand} {
		if strings.Contains(after, own) {
			t.Errorf("a sub-agent of the live session is on its page (%s):\n%s", own, after)
		}
	}
	if !strings.Contains(after, prior) {
		t.Errorf("the session that settled it is not on the page:\n%s", after)
	}

	// The grandchild asking through a hook counts the root as itself too.
	lin := askerLineage(dir, grand, "")
	for _, own := range []string{grand, child, parent} {
		if !lin[own] {
			t.Errorf("the grandchild's lineage misses %s: %v", own, lin)
		}
	}
	if lin[prior] {
		t.Errorf("the grandchild's lineage took an unrelated session: %v", lin)
	}
}
