package main

import (
	"os"
	"strings"
	"testing"
)

// The default agent contract is history-first. Check the rendered initialize
// text and every installable skill surface instead of generator constants, so
// a future experimental ctx feature cannot silently become mandatory guidance.
func TestDefaultAgentInstructionsKeepHistoryRecallFirst(t *testing.T) {
	initialized, code, message := handleMCP(t.TempDir(), rpcRequest{Method: "initialize"})
	if code != 0 {
		t.Fatalf("initialize: %d %s", code, message)
	}
	result, ok := initialized.(map[string]any)
	if !ok {
		t.Fatalf("initialize result = %T", initialized)
	}
	handshake, _ := result["instructions"].(string)
	assertInitializeHistoryContract(t, "MCP initialize", handshake)

	hermeticEnv(t)
	if _, err := writeCLISkill(); err != nil {
		t.Fatal(err)
	}
	cli, err := os.ReadFile(cliSkillPath())
	if err != nil {
		t.Fatal(err)
	}
	assertHistorySkillContract(t, cliSkillPath(), string(cli))

	seen := map[string]bool{}
	for _, target := range installTargetNames() {
		harness := guidanceHarness(target)
		path := guidancePath(harness)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if _, err := guidanceResult(target, false); err != nil {
			t.Fatalf("install guidance for %s: %v", target, err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read installed guidance for %s at %s: %v", target, path, err)
		}
		assertHistorySkillContract(t, path, string(body))
	}
}

// Marketplace/plugin copies bypass the installer, so their checked-in text is
// part of the default product contract too.
func TestBundledAgentSkillsKeepHistoryRecallFirst(t *testing.T) {
	for _, rel := range []string{
		"skills/deja-search/SKILL.md",
		"claude-plugin/skills/deja-history/SKILL.md",
		"codex-plugin/skills/deja-history/SKILL.md",
		"extensions/grok/skills/deja-history/SKILL.md",
		"extensions/kimi/skills/deja-history/SKILL.md",
	} {
		assertHistorySkillContract(t, rel, string(repoFile(t, rel)))
	}
}

func assertInitializeHistoryContract(t *testing.T, surface, body string) {
	t.Helper()
	for _, want := range []string{
		"deja indexes this user's past sessions", "Call the deja tool with mode recall",
		"before debugging an error", "might already exist",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s omits history-first term %q", surface, want)
		}
	}
	assertNoDefaultCtxLifecycle(t, surface, body)
}

func assertHistorySkillContract(t *testing.T, surface, body string) {
	t.Helper()
	for _, want := range []string{"deja", "recall"} {
		if !strings.Contains(body, want) {
			t.Errorf("%s omits history-first term %q", surface, want)
		}
	}
	assertNoDefaultCtxLifecycle(t, surface, body)
}

func assertNoDefaultCtxLifecycle(t *testing.T, surface, body string) {
	t.Helper()
	for _, forbidden := range []string{"ctx_resume", "ctx_checkpoint", "deja ctx resume", "deja ctx checkpoint"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("%s turns experimental ctx into default guidance with %q", surface, forbidden)
		}
	}
}

func TestCtxIsExplicitlyAvailableWithoutChangingDefaultGuidance(t *testing.T) {
	for _, action := range []string{"resume", "checkpoint", "status", "refresh", "history", "promote"} {
		if !isCtxCacheCommand(action) {
			t.Errorf("explicit ctx action %q is unavailable", action)
		}
	}
	for _, rel := range []string{"README.md", "docs/ctx-cache.md", "docs/guide/agents.html"} {
		body := string(repoFile(t, rel))
		for _, want := range []string{"experimental", "explicit", "ctx"} {
			if !strings.Contains(strings.ToLower(body), want) {
				t.Errorf("%s does not mark ctx opt-in with %q", rel, want)
			}
		}
	}
}
