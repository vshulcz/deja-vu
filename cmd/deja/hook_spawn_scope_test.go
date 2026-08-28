package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The spawn hook feeds a subagent's instructions through the per-prompt recall,
// so it inherits that path's project scope — the one #2333 narrowed. A
// subagent started in /work/api must not be handed a client's acme/api work,
// and the memory it does get is its own project's.
func TestSpawnRecallStaysInsideTheProject(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write := func(dirName, id, cwd, user, assistant string, hoursAgo int) {
		d := filepath.Join(root, dirName)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"user","content":%q}}`, id, at, cwd, user),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":%q}}`, id, at, assistant),
		}
		if err := os.WriteFile(filepath.Join(d, id+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Filler in my project, so a rare word is rare.
	for i, topic := range []string{"auth token expiry", "css grid overflow", "docker cache miss",
		"sql deadlock", "prometheus scrape", "goroutine leak", "npm advisory", "tls chain",
		"json schema", "redis eviction", "dns timeout", "kafka rebalance", "s3 throttling",
		"gc pause", "flaky ci runner", "proxy header"} {
		write("-work-api", fmt.Sprintf("mine%d", i), "/work/api",
			"my own "+topic+" question", "we settled the "+topic, 30+i)
	}
	// Only the client's project knows the rare word.
	for i := 0; i < 3; i++ {
		write("-clients-acme-api", fmt.Sprintf("acme%d", i), "/clients/acme/api",
			"the quaxbolt overflow in the acme ledger",
			"we decided the acme cutover runs at midnight", 1+i)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	spawn := func(prompt, sid string) string {
		t.Helper()
		payload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Task",`+
			`"tool_input":{"prompt":%q,"subagent_type":"general"},"cwd":"/work/api","session_id":%q}`, prompt, sid)
		var out bytes.Buffer
		if err := runHookTool(dir, strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			return ""
		}
		var resp struct {
			HookSpecificOutput struct {
				UpdatedInput map[string]json.RawMessage `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("bad reply %q: %v", out.String(), err)
		}
		var text string
		if err := json.Unmarshal(resp.HookSpecificOutput.UpdatedInput["prompt"], &text); err != nil {
			t.Fatalf("no prompt in the reply %q: %v", out.String(), err)
		}
		return text
	}

	// The premise: this surface does inject when the project knows the answer.
	own := spawn("look into the tls chain problem", "parent1")
	if !strings.Contains(own, "tls chain") {
		t.Fatalf("a subagent got no memory for its own project's topic, so this measures nothing:\n%s", own)
	}

	// And a word only another project knows brings nothing across. Asserted on
	// what was recorded rather than on the wording: the block that carries a
	// cross-project answer is sometimes the short "this project has history on
	// …" teaser, and a test that reads the prose can pass while the projects
	// behind it say otherwise.
	other := spawn("look into the quaxbolt overflow", "parent2")
	for _, sn := range usage.Snapshots(dir, 0) {
		for _, project := range sn.Projects {
			if project != "work/api" {
				t.Errorf("a subagent in /work/api was served %q:\n%s", project, other)
			}
		}
	}
}
