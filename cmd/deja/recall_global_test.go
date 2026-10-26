package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestRecallGlobalOptIn(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	work := t.TempDir()
	local := filepath.Join(work, "smokebeta")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct{ id, project, fact string }{
		{"global-active", "retiredalpha", "match.go tolerance reconcile setting CORPUSACTIVE51: use the violet active ledger."},
		{"local-distractor", "smokebeta", "Rule: every change in cmd/reconcile is checked with the dry-run replay of the August bank file; use the amber local ledger."},
	} {
		cwd := filepath.Join(work, seed.project)
		line, err := json.Marshal(map[string]any{
			"type": "assistant", "sessionId": seed.id, "cwd": cwd, "timestamp": "2026-10-25T10:00:00Z",
			"message": map[string]any{"role": "assistant", "content": seed.fact},
		})
		if err != nil {
			t.Fatal(err)
		}
		writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(cwd), seed.id+".jsonl"), seed.id, []string{string(line)})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(local)
	for _, tc := range []struct{ name, env, project, harness, want, absent string }{
		{"default", "", "", "", "amber local ledger", "violet active ledger"},
		{"zero", "0", "", "", "amber local ledger", "violet active ledger"},
		{"true_is_not_one", "true", "", "", "amber local ledger", "violet active ledger"},
		{"global", "1", "", "", "violet active ledger", ""},
		{"explicit_local", "1", "smokebeta", "claude", "amber local ledger", "violet active ledger"},
		{"explicit_remote", "1", "retiredalpha", "claude", "violet active ledger", "amber local ledger"},
		{"other_harness", "1", "", "codex", "", "violet active ledger"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEJA_RECALL_GLOBAL", tc.env)
			args, argsErr := json.Marshal(map[string]any{
				"mode": "recall", "q": "match.go tolerance reconcile setting CORPUSACTIVE51", "limit": 20,
				"project": tc.project, "harness": tc.harness,
			})
			if argsErr != nil {
				t.Fatal(argsErr)
			}
			if tc.project == "" && tc.harness == "" {
				items, _, err := contextSearchItems(dir, "match.go tolerance reconcile setting CORPUSACTIVE51")
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(items)
				if err != nil {
					t.Fatal(err)
				}
				text := string(encoded)
				if !strings.Contains(text, tc.want) || (tc.absent != "" && strings.Contains(text, tc.absent)) {
					t.Errorf("context_search: want %q, absent %q:\n%s", tc.want, tc.absent, text)
				}
			}
			searches := countRecallSearches(t)
			text, err := callMCPTool(dir, "deja", args)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, tc.want) || (tc.absent != "" && strings.Contains(text, tc.absent)) {
				t.Fatalf("want %q, absent %q:\n%s", tc.want, tc.absent, text)
			}
			if tc.name == "global" {
				if !strings.Contains(text, "global-active") || !strings.Contains(text, "retiredalpha") {
					t.Fatalf("global fact lost its session/project attribution:\n%s", text)
				}
				if *searches != 1 {
					t.Errorf("global recall ran %d searches, want 1", *searches)
				}
			}
		})
	}
}
