package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// DEJA_AUTORECALL_LOCAL_ONLY=1 keeps synced sessions out of the startup
// digest while leaving them searchable.
func TestAutoRecallLocalOnly(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-isoproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"loc1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"local isoproj work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "loc1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.EnsureForSearch(dir, search.Options{Query: "isoproj"}, false, nil); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(batch, "deja-sync-x-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	_ = enc.Encode(index.SyncRecord{Harness: "claude", SessionID: "rem1", Project: "tmp/isoproj", Role: "user", Text: "remote isoproj work", Time: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)})
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := index.Import(dir, batch); err != nil || n != 1 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/isoproj")

	digest := hookDigest(index.DefaultDir())
	if !strings.Contains(digest, "imported:") {
		t.Fatalf("default digest should include imported session, got: %q", digest)
	}
	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	digest = hookDigest(index.DefaultDir())
	if strings.Contains(digest, "imported:") {
		t.Fatalf("local-only digest leaked imported session: %q", digest)
	}
	if !strings.Contains(digest, "isoproj") {
		t.Fatalf("local-only digest lost local session: %q", digest)
	}
}

func TestPolicyFileBlocksImportedInSearchHits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"mcp":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hits := []search.Hit{
		{Session: model.Session{ID: "a", Project: "deja-vu"}},
		{Session: model.Session{ID: "b", Project: "imported:mini/deja-vu"}},
	}
	got := policyFilterHits(policy.ActivationMCP, hits)
	if len(got) != 1 || got[0].Session.ID != "a" {
		t.Fatalf("mcp filter wrong: %#v", got)
	}
	// The search path has no rule in this file, so nothing is dropped.
	hits2 := []search.Hit{
		{Session: model.Session{ID: "a", Project: "deja-vu"}},
		{Session: model.Session{ID: "b", Project: "imported:mini/deja-vu"}},
	}
	if got := policyFilterHits(policy.ActivationSearch, hits2); len(got) != 2 {
		t.Fatalf("search filter must pass all: %#v", got)
	}
}

func TestLogLastNamesPolicy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	usage.RecordDigestPolicy(dir, usage.KindHook, "digest body", 2, 100, "local-only")
	var out bytes.Buffer
	if err := runLogTo(&out, dir, []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "policy: local-only") {
		t.Fatalf("log --last must name the policy:\n%s", out.String())
	}
}
