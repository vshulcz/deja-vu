package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/ctxcache"
)

func ctxFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "synthetic", "ctx", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ctxFixtureEnvironment(t *testing.T) (workspace, indexDir string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_CTX_DIR", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	workspace = filepath.Join(root, "repo")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	return workspace, filepath.Join(root, "index")
}

func ctxFixtureCLI(t *testing.T, dir, workspace, action string, state []byte, extra ...string) []byte {
	t.Helper()
	args := append([]string{action, "--workspace", workspace, "--task", "FIXTURE-1"}, extra...)
	var out strings.Builder
	if err := runCtxCache(dir, args, strings.NewReader(string(state)), &out); err != nil {
		t.Fatal(err)
	}
	return []byte(out.String())
}

func TestCtxCachePublicBudgetPreservesRequiredContext(t *testing.T) {
	workspace, dir := ctxFixtureEnvironment(t)
	state := ctxcache.State{Objective: "Preserve required context", Gaps: []ctxcache.Gap{{Subject: "validate telemetry", Severity: "required", Reason: "the source has not been checked", RetrievalHint: "inspect the telemetry test results", Source: "file://synthetic/test-results.json"}}, Session: []ctxcache.Item{{ID: "scratch", Text: strings.Repeat("temporary detail ", 2000), Source: "file://synthetic/session"}}}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	ctxFixtureCLI(t, dir, workspace, "checkpoint", b)
	const budget = 6000
	cli := ctxFixtureCLI(t, dir, workspace, "resume", nil, "--budget", fmt.Sprint(budget))
	args, _ := json.Marshal(map[string]any{"mode": "ctx_resume", "workspace": workspace, "task_id": "FIXTURE-1", "token_budget": budget})
	mcp, err := callMCPTool(dir, "deja", args)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"CLI": cli, "MCP": []byte(mcp)} {
		if len(raw) > budget {
			t.Errorf("%s emitted %d bytes for conservative budget %d", name, len(raw), budget)
		}
		var result ctxcache.ResumeResult
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if !result.Truncated || result.Snapshot.State.Objective != state.Objective {
			t.Fatalf("%s lost objective or did not bound fixture: %s", name, raw)
		}
		if len(result.Snapshot.State.Gaps) != 1 || result.Snapshot.State.Gaps[0].Subject != "validate telemetry" || result.Snapshot.State.Gaps[0].Severity != "required" {
			t.Fatalf("%s omitted required gap: %s", name, raw)
		}
	}
	if string(cli) != mcp {
		t.Fatalf("CLI/MCP serialized different resume packets\nCLI=%s\nMCP=%s", cli, mcp)
	}
}

func TestCtxCachePublicRejectsLossyCheckpointInput(t *testing.T) {
	workspace, dir := ctxFixtureEnvironment(t)
	for _, input := range []string{`null`, `[]`, `{"objectiv":"typo"}`, `{"objective":"one","objective":"two"}`, `{"objective":"one"} {"objective":"two"}`} {
		t.Run(input, func(t *testing.T) {
			var out strings.Builder
			if err := runCtxCache(dir, []string{"checkpoint", "--workspace", workspace}, strings.NewReader(input), &out); err == nil {
				t.Fatalf("CLI accepted %s", input)
			}
			if !json.Valid([]byte(input)) {
				return
			} // malformed outer MCP JSON is rejected by the transport
			args := json.RawMessage(`{"workspace":` + quoteJSON(workspace) + `,"state":` + input + `}`)
			if _, err := callMCPTool(dir, "deja_ctx_checkpoint", args); err == nil {
				t.Fatalf("MCP accepted %s", input)
			}
		})
	}
}

func TestCtxCachePublicBudgetValidation(t *testing.T) {
	workspace, dir := ctxFixtureEnvironment(t)
	for _, value := range []string{"0", "-1", "0.5", "6000.5", "9223372036854775808", "null"} {
		var out strings.Builder
		if err := runCtxCache(dir, []string{"resume", "--workspace", workspace, "--budget", value}, strings.NewReader(""), &out); err == nil {
			t.Errorf("CLI accepted budget %s", value)
		}
		args := json.RawMessage(`{"workspace":` + quoteJSON(workspace) + `,"token_budget":` + value + `}`)
		if _, err := callMCPTool(dir, "ctx_resume", args); err == nil {
			t.Errorf("MCP accepted budget %s", value)
		}
	}
	for _, raw := range []string{`6000`, `"6000"`} {
		args := json.RawMessage(`{"workspace":` + quoteJSON(workspace) + `,"token_budget":` + raw + `}`)
		if _, err := callMCPTool(dir, "ctx_resume", args); err != nil {
			t.Errorf("MCP rejected integer budget %s: %v", raw, err)
		}
	}
}

func TestCtxCacheLocalSourceFixtureRefreshesOnlyTask(t *testing.T) {
	workspace, dir := ctxFixtureEnvironment(t)
	path := filepath.Join(t.TempDir(), "task.json")
	if err := os.WriteFile(path, ctxFixture(t, "task-in-progress.json"), 0600); err != nil {
		t.Fatal(err)
	}
	state := ctxcache.State{Project: map[string]any{"architecture": "unchanged project"}, Sources: []ctxcache.Source{{Name: "task-fixture", Layer: "task", Path: path}}}
	b, _ := json.Marshal(state)
	ctxFixtureCLI(t, dir, workspace, "checkpoint", b)
	if err := os.WriteFile(path, ctxFixture(t, "task-validated.json"), 0600); err != nil {
		t.Fatal(err)
	}
	var status ctxcache.Status
	if err := json.Unmarshal(ctxFixtureCLI(t, dir, workspace, "status", nil), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "stale" {
		t.Fatalf("changed local source reported %s", status.Status)
	}
	refreshed := ctxFixtureCLI(t, dir, workspace, "refresh", nil)
	if !strings.Contains(string(refreshed), `"validated"`) {
		t.Fatalf("refresh did not apply local source: %s", refreshed)
	}
	var packet ctxcache.ResumeResult
	if err := json.Unmarshal(ctxFixtureCLI(t, dir, workspace, "resume", nil), &packet); err != nil {
		t.Fatal(err)
	}
	if packet.Snapshot.State.Status != "validated" || len(packet.Snapshot.State.Failing) != 0 || packet.Snapshot.State.Project["architecture"] != "unchanged project" {
		t.Fatalf("source materialization lost isolation: %#v", packet.Snapshot.State)
	}
	ctxFixtureCLI(t, dir, workspace, "promote", nil, "--item", "fact-freshness", "--to", "project")
	diff := ctxFixtureCLI(t, dir, workspace, "diff", nil)
	if !strings.Contains(string(diff), "fact-freshness") {
		t.Fatalf("promotion missing from semantic diff: %s", diff)
	}
	explanation := ctxFixtureCLI(t, dir, workspace, "explain", nil, "--item", "fact-freshness")
	if !strings.Contains(string(explanation), "file://synthetic/test-results.json") {
		t.Fatalf("item explain missing provenance: %s", explanation)
	}
	for _, mode := range []string{"ctx_history", "ctx_promote"} {
		args, _ := json.Marshal(map[string]any{"mode": mode, "workspace": workspace, "task_id": "FIXTURE-1", "item_id": "fact-freshness", "to": "task"})
		if _, err := callMCPTool(dir, "deja", args); err != nil {
			t.Errorf("%s: %v", mode, err)
		}
	}
}
