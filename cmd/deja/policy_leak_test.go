package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	search "github.com/vshulcz/deja-vu/internal/search"
)

// policyLeakIndex builds an index holding one imported (peer) session and
// points CLAUDE_PROJECT_DIR at a matching local project.
func policyLeakIndex(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	fresh := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-isoproj", "local.jsonl"), "local1", []string{
		`{"type":"user","sessionId":"local1","timestamp":"` + fresh + `","message":{"role":"user","content":"local work on the parser"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A peer session arriving over sync.
	batch := t.TempDir()
	line := `{"harness":"claude","session_id":"peer1","project":"isoproj","role":"user","text":"authentication middleware panicked in handler_test.go ZQSECRETMARKER","time":"` + fresh + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "b.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "isoproj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	return dir
}

// DEJA_RECALL=off is documented as the kill switch; a warm cache must not
// keep injecting after it is set.
func TestHookCacheHonorsRecallOff(t *testing.T) {
	dir := policyLeakIndex(t)
	if d, _, _, _, _, _ := cachedHookDigest(dir); d == "" {
		t.Fatal("expected a digest before the kill switch")
	}
	t.Setenv("DEJA_RECALL", "off")
	if d, _, _, _, _, _ := cachedHookDigest(dir); d != "" {
		t.Fatalf("cache served a digest with DEJA_RECALL=off:\n%s", d)
	}
}

// A digest cached under one policy must not be served under another: the
// cache hit returns before the policy is ever consulted.
func TestHookCacheRefusesEntryFromAnotherPolicy(t *testing.T) {
	dir := policyLeakIndex(t)
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	// Warm the cache under the permissive policy.
	warm, _, _, _, _, _ := cachedHookDigest(dir)
	if warm == "" {
		t.Fatal("expected a digest to cache")
	}
	// Plant a recognizable entry as if it had been built then.
	planted := "PLANTED-UNDER-OLD-POLICY"
	writeHookCache(dir, cwd, planted, 1, 10, nil, 0, nil)
	if got, _, _, _, _, _ := cachedHookDigest(dir); got != planted {
		t.Fatalf("cache did not serve its own entry back: %q", got)
	}
	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	if got, _, _, _, _, _ := cachedHookDigest(dir); got == planted {
		t.Fatal("cache served an entry built under a policy that no longer applies")
	}
}

// The per-prompt déjà vu hook injects like any other path and must ask the
// policy too.
func TestPromptHookHonorsPolicy(t *testing.T) {
	dir := policyLeakIndex(t)
	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	in := strings.NewReader(`{"session_id":"s","cwd":"` + os.Getenv("CLAUDE_PROJECT_DIR") + `","prompt":"why did authentication middleware panic in handler_test.go"}`)
	var out strings.Builder
	if err := runHookPrompt(dir, in, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "ZQSECRETMARKER") {
		t.Fatalf("prompt hook injected policy-denied memory:\n%s", out.String())
	}
}

// blame carries whole sessions, so it is the path that most needs the rule.
func TestBlameHonorsPolicy(t *testing.T) {
	dir := policyLeakIndex(t)
	// Deny imported memory on every activation via a policy file.
	pol := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"imported":false},"mcp":{"imported":false},"auto":{"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)
	got, _, err := blameTextResult(dir, search.BlameOptions{All: true}, "handler_test.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "ZQSECRETMARKER") {
		t.Fatalf("blame served policy-denied memory:\n%s", got)
	}
}

// ctx is the command the hook tells an agent to call, and it answered from
// sessions every other reading surface withheld (#1026).
func TestCtxHonorsPolicy(t *testing.T) {
	dir := policyLeakIndex(t)
	pol := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"imported":false},"mcp":{"imported":false},"auto":{"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)
	out := captureStdout(t, func() {
		if err := cmdCtx(dir, []string{"authentication", "middleware", "panicked"}); err == nil {
			t.Error("ctx answered from a session the policy denies")
		}
	})
	if strings.Contains(out, "ZQSECRETMARKER") {
		t.Fatalf("ctx served policy-denied memory:\n%s", out)
	}
	// The id-prefix branch takes its own path to the same session.
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, m := range metas {
		if strings.HasPrefix(m.Project, "imported:") && len(m.ID) >= 6 {
			id = m.ID
		}
	}
	if id == "" {
		t.Fatal("no imported session with an id long enough for the prefix branch")
	}
	out = captureStdout(t, func() {
		if err := cmdCtx(dir, []string{id}); err == nil {
			t.Error("ctx by id answered from a session the policy denies")
		}
	})
	if strings.Contains(out, "ZQSECRETMARKER") {
		t.Fatalf("ctx by id served policy-denied memory:\n%s", out)
	}
}
