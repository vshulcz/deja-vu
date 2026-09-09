package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestCtxCacheCLIIsLocalAndPreservesLegacyCtx(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_CTX_DIR", filepath.Join(root, "ctx"))
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	state := `{"objective":"finish cache","confirmed":[{"id":"fact","text":"resume is local","source":"file://spec"}]}`
	var out strings.Builder
	if err := runCtxCache(filepath.Join(root, "index.db"), []string{"checkpoint", "--workspace", repo, "--task", "T-1", "--state", state}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runCtxCache(filepath.Join(root, "index.db"), []string{"resume", "--workspace", repo, "--task", "T-1", "--budget", "10000"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	var result struct {
		CacheStatus string `json:"cache_status"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatal(err)
	}
	if result.CacheStatus != "hit" {
		t.Fatalf("resume=%s", out.String())
	}
	if isCtxCacheCommand("pool") {
		t.Fatal("ordinary historical query became a cache subcommand")
	}
}

func TestCtxCacheMCPModesAndAliases(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_CTX_DIR", filepath.Join(root, "ctx"))
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	checkpoint := `{"workspace":` + quoteJSON(repo) + `,"task_id":"T-1","state":{"objective":"ship","failing":[{"id":"test","text":"fails","source":"file://test"}]}}`
	if got, err := callMCPTool(filepath.Join(root, "index.db"), "deja_ctx_checkpoint", json.RawMessage(checkpoint)); err != nil || !strings.Contains(got, "snapshot_id") {
		t.Fatalf("checkpoint=%q %v", got, err)
	}
	args := json.RawMessage(`{"workspace":` + quoteJSON(repo) + `,"task_id":"T-1"}`)
	for _, name := range []string{"ctx_resume", "deja_ctx_status", "ctx_refresh", "ctx_explain", "ctx_invalidate"} {
		callArgs := args
		if strings.Contains(name, "explain") {
			callArgs = json.RawMessage(`{"workspace":` + quoteJSON(repo) + `,"task_id":"T-1","item_id":"test"}`)
		}
		if _, err := callMCPTool(filepath.Join(root, "index.db"), name, callArgs); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The cache opt-in must not reserve words from the existing historical query
// interface. Exercise real command dispatch against a real indexed session.
func TestCtxCacheActionWordsRemainHistoricalQueries(t *testing.T) {
	root := hermeticEnv(t)
	cache := filepath.Join(root, "working-context")
	t.Setenv("DEJA_CTX_DIR", cache)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0700); err != nil {
		t.Fatal(err)
	}
	const text = "resume refresh checkpoint status diff explain invalidate history lookup promote: the saved decision was to keep the old transport"
	record := `{"type":"user","message":{"role":"user","content":` + quoteJSON(text) + `},"timestamp":"2026-08-04T10:00:00Z","sessionId":"legacy-ctx-query","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "session.jsonl"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"resume", "refresh", "checkpoint", "status", "diff", "explain", "invalidate", "history", "lookup", "promote"} {
		out, err := captureRun(t, "ctx", query)
		if err != nil || !strings.Contains(out, "keep the old transport") {
			t.Errorf("ctx %s lost historical query: %v\n%s", query, err, out)
		}
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("ordinary history queries created a context cache: %v", err)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "ctx", "--cache", "checkpoint", "--workspace", workspace, "--state", `{"objective":"explicit working state"}`); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "ctx", "--cache", "resume", "--workspace", workspace)
	if err != nil || !strings.Contains(out, `"objective":"explicit working state"`) {
		t.Fatalf("explicit cache resume: %v\n%s", err, out)
	}
	if _, err := captureRun(t, "ctx", "--cache"); err == nil {
		t.Fatal("cache opt-in without an action succeeded")
	}
}
