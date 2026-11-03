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

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestHookToolApplyPatchSupportsPatchTextAndPostToolUse(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"s1", "s2", "s3"} {
		ts := time.Date(2026, 8, 1+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work","timestamp":"` + ts + `","message":{"role":"user","content":"update pkg/billing/invoice.go"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Edit","input":{"file_path":"/work/pkg/billing/invoice.go","old_string":"a","new_string":"b"}},{"type":"text","text":"Decision: keep invoice rounding in pkg/billing/invoice.go deterministic."}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "apply_patch",
		"tool_input": map[string]any{
			"patchText": "*** Begin Patch\n*** Update File: pkg/billing/invoice.go\n@@\n-a\n+b\n*** End Patch",
		},
		"session_id": "live-sess",
		"cwd":        "/work",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runHookTool(indexDir, strings.NewReader(string(raw)), &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "invoice.go has been worked on in 3 sessions") {
		t.Fatalf("expected apply_patch PostToolUse with patchText to emit file history line, got: %q", stdout.String())
	}
}

func TestFileHookLineOutsideResolvesProjectFromChildFilePath(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	homeDir := filepath.Join(tmp, "home")
	repoDir := filepath.Join(homeDir, "coding", "myrepo")
	targetFile := filepath.Join(repoDir, "pkg", "engine.go")
	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetFile, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-myrepo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"m1", "m2", "m3"} {
		ts := time.Date(2026, 8, 10+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		// Relative path "pkg/engine.go" stored under project "myrepo", while the
		// live agent session was started from homeDir and accesses targetFile by
		// its absolute path inside homeDir.
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"check pkg/engine.go"}}`, id, repoDir, ts),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Read","input":{"file_path":"pkg/engine.go"}},{"type":"text","text":"Decision: keep pkg/engine.go stateless across retries."}]}}`, id, repoDir, ts),
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	line := fileHookLineOutside(indexDir, homeDir, targetFile, nil)
	if !strings.Contains(line, "engine.go has been worked on in 3 sessions") {
		t.Fatalf("expected child-repo project resolution from parent cwd %q, got: %q", homeDir, line)
	}
}

func TestCompactionContextIncludesModifiedFilesInTranscriptOrder(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s := model.Session{
		ID:      "comp-mod",
		Harness: "opencode",
		Project: "deja-vu",
		Messages: []model.Message{
			{Role: "user", Text: "Refactor T1 through T4 across packages", Time: at},
			{Role: sources.RoleEdit, Text: "pkg/t1/t1.go\n-old\n+new", Time: at.Add(time.Minute)},
			{Role: sources.RoleWrote, Text: "pkg/t2/t2.go\npackage t2", Time: at.Add(2 * time.Minute)},
			{Role: sources.RoleEdit, Text: "pkg/t1/t1.go\n-new\n+newer", Time: at.Add(3 * time.Minute)},
			{Role: "assistant", Text: "Decision: completed T1 and T2; Gap: T3 and T4 remain.", Time: at.Add(4 * time.Minute)},
		},
	}
	c := digest.ExtractCompactionContext(s, digest.ExtractOptions{})
	if len(c.Modified) != 2 || c.Modified[0].Text != "pkg/t2/t2.go" || c.Modified[1].Text != "pkg/t1/t1.go" {
		t.Fatalf("unexpected Modified ledger: %#v", c.Modified)
	}
	rendered := digest.RenderCompactionContext(c, 2400)
	if !strings.Contains(rendered, "Modified in this session\n- pkg/t2/t2.go [opencode:comp-mod/wrote") ||
		!strings.Contains(rendered, "- pkg/t1/t1.go [opencode:comp-mod/edit") {
		t.Fatalf("expected rendered compaction packet to include Modified in this session, got:\n%s", rendered)
	}
}

func TestShownInjectionWarningSuppressesUnshownEarlierAttempts(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	candidates := []model.Session{
		{
			ID: "s-new", Harness: "opencode", Project: "p", Updated: t0.Add(72 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "switch the pool back — transaction mode broke prepared statements pgbouncer"},
			},
		},
		{
			ID: "s-old-unshown", Harness: "opencode", Project: "p", Updated: t0,
			Messages: []model.Message{
				{Role: "user", Text: "switch the pool to transaction mode for pgbouncer latency"},
			},
		},
	}
	if warn := shownInjectionWarning(candidates, candidates[:1]); warn != "" {
		t.Fatalf("expected no ghost warning when s-old-unshown is not in shown slice, got: %q", warn)
	}
	if warn := shownInjectionWarning(candidates, candidates); !strings.Contains(warn, "session s-old-unshown is an earlier attempt") {
		t.Fatalf("expected earlier-attempt warning when s-old-unshown is shown, got: %q", warn)
	}
}

func TestStemmedLongQueryWithCoveredSubjectIsNotDisowned(t *testing.T) {
	var b strings.Builder
	terms := []string{"docs/guide.md", "feed", "page", "edit", "quuxify"}
	held := []string{"docs/guide.md", "feed", "page", "edit"}
	idf := map[string]float64{
		"docs/guide.md": 4.5,
		"feed":          3.2,
		"page":          2.0,
		"edit":          1.5,
		"quuxify":       0,
	}
	cover := recallCoverage{
		at:     0,
		held:   held,
		terms:  terms,
		idf:    idf,
		weight: 4.5 + 3.2 + 2.0 + 1.5,
	}
	res := index.SearchResult{
		Tier:    search.TierStemmed,
		Stemmed: true,
		Variants: map[string][]string{
			"edit":    {"edited"},
			"quuxify": nil,
		},
		TermIDF: idf,
	}
	namesTheAsked, saidNothing := writeRecallLead(&b, res, nil, "docs/guide.md feed page edit quuxify", 0, true, false, cover, false)
	if saidNothing || strings.Contains(b.String(), nothingIsAboutThis) {
		t.Fatalf("expected stemmed query with strong coverage not to be disowned, got:\n%s", b.String())
	}
	if !namesTheAsked {
		t.Fatalf("expected namesTheAsked=true when cover.aboutIt() holds")
	}
}

func TestFixesForCommandIgnoresUnrelatedPairOnGenericNotGitRepoError(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","sessionId":"s-git","cwd":"/work","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"check repo"}}`,
		`{"type":"assistant","sessionId":"s-git","cwd":"/work","timestamp":"2026-08-01T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Bash","input":{"command":"git rev-parse --show-toplevel"}}]}}`,
		`{"type":"user","sessionId":"s-git","cwd":"/work","timestamp":"2026-08-01T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u1","is_error":true,"content":"fatal: not a git repository (or any of the parent directories): .git"}]}}`,
		`{"type":"assistant","sessionId":"s-git","cwd":"/work","timestamp":"2026-08-01T10:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"u2","name":"Bash","input":{"command":"sha256sum README.md"}}]}}`,
		`{"type":"user","sessionId":"s-git","cwd":"/work","timestamp":"2026-08-01T10:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u2","content":"deadbeef README.md"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s-git.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got := index.FixesForCommand(indexDir, "git status --short", "fatal: not a git repository (or any of the parent directories): .git", 3, nil)
	if len(got) != 0 {
		t.Fatalf("expected unrelated command after generic 'not a git repository' error to be filtered out, got: %#v", got)
	}
}
