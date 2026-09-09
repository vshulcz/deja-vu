package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/ctxcache"
)

func ctxListedTool(t *testing.T) map[string]any {
	t.Helper()
	result, code, msg := handleMCP(t.TempDir(), rpcRequest{Method: "tools/list"})
	if code != 0 {
		t.Fatalf("tools/list: %d %s", code, msg)
	}
	return result.(map[string]any)["tools"].([]map[string]any)[0]
}

func TestCtxMCPAdvertisingRequiresExplicitOptIn(t *testing.T) {
	workspace, dir := ctxFixtureEnvironment(t)
	// Using the CLI cache must not change the schema for unrelated MCP clients.
	ctxFixtureCLI(t, dir, workspace, "checkpoint", []byte(`{"objective":"local state"}`))
	for _, value := range []string{"", "0", "true", "1", ""} {
		t.Setenv("DEJA_CTX_MCP", value)
		tool := ctxListedTool(t)
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		modes := props["mode"].(map[string]any)["enum"].([]string)
		if value != "1" {
			if !reflect.DeepEqual(modes, []string{"recall", "context", "blame", "fix", "how", "remember"}) {
				t.Fatalf("default modes changed: %v", modes)
			}
			if len(props) != 13 || strings.Contains(tool["description"].(string), "ctx_") {
				t.Fatalf("default schema includes experimental overhead: %#v", tool)
			}
			continue
		}
		for action := range ctxCacheCommands {
			mode := "ctx_" + action
			if !containsMode(modes, mode) || dispatcherModes[mode] != mode {
				t.Errorf("opt-in schema/dispatcher omit %s", mode)
			}
		}
		for _, prop := range []string{"workspace", "task_id", "token_budget", "component_versions", "state", "item_id", "layer", "source", "to", "keep"} {
			if props[prop] == nil {
				t.Errorf("opt-in schema omits %s", prop)
			}
		}
	}
}

func containsMode(modes []string, want string) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}

func TestCtxPrunePublicCLIAndMCP(t *testing.T) {
	for _, surface := range []string{"CLI", "MCP"} {
		t.Run(surface, func(t *testing.T) {
			workspace, dir := ctxFixtureEnvironment(t)
			for i := 0; i < 3; i++ {
				ctxFixtureCLI(t, dir, workspace, "checkpoint", []byte(fmt.Sprintf(`{"objective":"state %d"}`, i)))
			}
			var raw []byte
			if surface == "CLI" {
				raw = ctxFixtureCLI(t, dir, workspace, "prune", nil, "--keep", "1")
			} else {
				args, _ := json.Marshal(map[string]any{"mode": "ctx_prune", "workspace": workspace, "task_id": "FIXTURE-1", "keep": 1})
				result, err := callMCPTool(dir, "deja", args)
				if err != nil {
					t.Fatal(err)
				}
				raw = []byte(result)
			}
			var result ctxcache.PruneResult
			if err := json.Unmarshal(raw, &result); err != nil || result.Removed != 2 || result.Kept != 1 {
				t.Fatalf("prune result %s: %v", raw, err)
			}
			var history []ctxcache.Snapshot
			if err := json.Unmarshal(ctxFixtureCLI(t, dir, workspace, "history", nil), &history); err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 || history[0].State.Objective != "state 2" {
				t.Fatalf("prune did not preserve latest state: %#v", history)
			}
			ctxFixtureCLI(t, dir, workspace, "checkpoint", []byte(`{"objective":"after prune"}`))
			var resultPacket ctxcache.ResumeResult
			if err := json.Unmarshal(ctxFixtureCLI(t, dir, workspace, "resume", nil), &resultPacket); err != nil || resultPacket.Snapshot.Freshness.SnapshotVersion != 4 {
				t.Fatalf("version continuity lost after prune: %#v %v", resultPacket, err)
			}
		})
	}
}

func TestCtxPruneRejectsInvalidRetention(t *testing.T) {
	workspace, dir := ctxFixtureEnvironment(t)
	for _, value := range []string{"0", "-1", "1.5", "null", "9223372036854775808"} {
		var out strings.Builder
		if err := runCtxCache(dir, []string{"prune", "--workspace", workspace, "--keep", value}, strings.NewReader(""), &out); err == nil {
			t.Errorf("CLI accepted keep=%s", value)
		}
		args := json.RawMessage(`{"workspace":` + quoteJSON(workspace) + `,"keep":` + value + `}`)
		if _, err := callMCPTool(dir, "ctx_prune", args); err == nil {
			t.Errorf("MCP accepted keep=%s", value)
		}
	}
}
