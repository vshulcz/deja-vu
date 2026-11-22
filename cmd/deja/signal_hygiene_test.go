package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestPromptTaskTargetsSkipsConversationalCyrillicWithoutCorroboration(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "app")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":             "module example.com/app\n\ngo 1.24\n",
		"order_flow.go":      "package app\n\nfunc RunOrderFlow() {}\n",
		"localize_filter.go": "package app\n\nfunc FilterLocalizedSnippets() {}\n",
		"alpha_worker.go":    "package app\n\nfunc RunAlphaWorker() {}\n",
		"beta_worker.go":     "package app\n\nfunc RunBetaWorker() {}\n",
		"gamma_worker.go":    "package app\n\nfunc RunGammaWorker() {}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	targetOrder := filepath.Join(proj, "order_flow.go")
	for i, sid := range []string{"ru-hist-1", "ru-hist-2"} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "app", sid+".jsonl"), sid, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-09-0%dT10:00:00Z","message":{"role":"user","content":"разберём по порядку что у нас дальше"}}`, sid, proj, i+1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-09-0%dT10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e%d","name":"Edit","input":{"file_path":%q,"old_string":"RunOrderFlow()","new_string":"RunOrderFlow()"}}]}}`, sid, proj, i+1, i+1, targetOrder),
		})
	}
	if err := index.Ensure(idxDir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, conversational := range []string{
		"давай по порядку",
		"что у нас дальше по плану?",
	} {
		if got, _ := promptTaskTargetsLine(idxDir, proj, conversational, nil, nil); got != "" {
			t.Fatalf("promptTaskTargetsLine(%q) = %q, want empty without corroboration", conversational, got)
		}
	}

	if got, _ := promptTaskTargetsLine(idxDir, proj, "поправь локализацию и фильтрацию сниппетов", nil, nil); !strings.Contains(got, "localize_filter.go") {
		t.Fatalf("expected localize_filter.go for technical Cyrillic prompt, got %q", got)
	}
}

func TestLiveRulesSkipSubagentTranscriptsAndProbePrompts(t *testing.T) {
	if !isSubagentTranscriptPath("/work/app/.claude/projects/p/subagents/agent-1.jsonl") {
		t.Fatal("expected /subagents/ transcript path to be rejected")
	}
	if !isSubagentTranscriptPath("/tmp/agent-xyz.jsonl") {
		t.Fatal("expected agent-*.jsonl transcript path to be rejected")
	}
	if isSubagentTranscriptPath("/work/app/.claude/projects/p/main.jsonl") {
		t.Fatal("main transcript path should not be treated as subagent")
	}

	probes := []string{
		"<system_message>check</system_message>",
		"You are a code reviewer. Output only JSON.",
		"Subagent brief: investigate without editing any files.",
	}
	for _, p := range probes {
		if !isSubagentOrProbePrompt(p) {
			t.Errorf("isSubagentOrProbePrompt(%q) = false, want true", p)
		}
	}
	if isSubagentOrProbePrompt("Never run integration tests against live tokens in pkg/auth/client.go.") {
		t.Fatal("real standing rule misclassified as probe prompt")
	}

	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	repo := filepath.Join(tmp, "work", "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	maybePinLiveUserRule(idxDir, repo, "probe-1", "You are a reviewer. Never modify cmd/deja/repo.go.")
	syncLiveSessionRules(idxDir, repo, promptHookInput{
		SessionID:       "sub-1",
		ParentSessionID: "parent-1",
		Harness:         "claude",
	}, "Never suggest --force in cmd/deja/repo.go.")
	syncLiveSessionRules(idxDir, repo, promptHookInput{
		SessionID:      "sub-2",
		TranscriptPath: filepath.Join(tmp, "claude", "p", "subagents", "agent-2.jsonl"),
		Harness:        "claude",
	}, "Never suggest --force in cmd/deja/repo.go.")

	agySubTranscript := filepath.Join(tmp, "brain", "conv-sub", "subagents", "agent-9.jsonl")
	if err := os.MkdirAll(filepath.Dir(agySubTranscript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agySubTranscript, []byte(`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nNever use global locks in pkg/store/db.go.\n</USER_REQUEST>"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		inv  int
		conv string
	}{
		{inv: 0, conv: "conv-sub-0"},
		{inv: 1, conv: "conv-sub-1"},
	} {
		payload, err := json.Marshal(antigravityHookInput{
			InvocationNum:  tc.inv,
			WorkspacePaths: []string{repo},
			TranscriptPath: agySubTranscript,
			ConversationID: tc.conv,
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runHookAntigravity(idxDir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
	}

	if entries := readLiveRulesFile(filepath.Join(repo, ".deja", "live_rules.jsonl")); len(entries) != 0 {
		t.Fatalf("expected subagent/probe hooks to write 0 live rules, got %+v", entries)
	}

	syncLiveSessionRules(idxDir, repo, promptHookInput{
		SessionID: "main-1",
		Harness:   "claude",
	}, "Always run go test before editing internal/auth/service.go.")
	if entries := readLiveRulesFile(filepath.Join(repo, ".deja", "live_rules.jsonl")); len(entries) != 1 || len(entries[0].Files) == 0 || entries[0].Files[0] != "internal/auth/service.go" {
		t.Fatalf("expected real user standing rule to be pinned once with file scope, got %+v", entries)
	}
}

func TestIsCodeFilePathRejectsProseSlashesKeepsRepoPaths(t *testing.T) {
	notPaths := []string{
		"CI/CD",
		"да/нет",
		"чтение/запись",
		"hooks/mcp",
		"https://example.com/pkg/a.go",
		"file:///work/app/a.go",
		"foo...bar/baz",
		"12/34",
	}
	for _, s := range notPaths {
		if isCodeFilePath(s) {
			t.Errorf("isCodeFilePath(%q) = true, want false", s)
		}
	}

	valid := []string{
		"cmd/deja/repo.go",
		"cmd/deja",
		"internal/index/giveup.go",
		"Makefile",
	}
	for _, s := range valid {
		if !isCodeFilePath(s) {
			t.Errorf("isCodeFilePath(%q) = false, want true", s)
		}
	}
}

func TestAppendLiveRuleDedupesParaphrasesPreservesOppositePolarity(t *testing.T) {
	target := filepath.Join(t.TempDir(), "live_rules.jsonl")
	r1 := sources.RepoEntry{
		ID:    "r-1",
		Kind:  "rule",
		Text:  "Never use legacy retry loop in pkg/worker/runner.go",
		Files: []string{"pkg/worker/runner.go"},
		Date:  "2026-09-10",
		From:  "s1",
	}
	r2 := sources.RepoEntry{
		ID:    "r-2",
		Kind:  "rule",
		Text:  "Never keep legacy retry loop in pkg/worker/runner.go",
		Files: []string{"pkg/worker/runner.go"},
		Date:  "2026-09-11",
		From:  "s2",
	}
	r3 := sources.RepoEntry{
		ID:    "r-3",
		Kind:  "rule",
		Text:  "Always use legacy retry loop in pkg/worker/runner.go",
		Files: []string{"pkg/worker/runner.go"},
		Date:  "2026-09-12",
		From:  "s3",
	}

	appendLiveRule(target, r1, []string{"runner"})
	appendLiveRule(target, r2, []string{"runner"})
	appendLiveRule(target, r3, []string{"runner"})

	got := readLiveRuleRecords(target)
	if len(got) != 2 {
		t.Fatalf("len(readLiveRuleRecords) = %d, want 2 (got %+v)", len(got), got)
	}
	if got[0].ID != "r-2" || got[1].ID != "r-3" {
		t.Fatalf("kept records = %+v, want [r-2, r-3]", got)
	}
}

func TestInferRepoKindIgnoresQuotedMarkers(t *testing.T) {
	if got := inferRepoKind("Documented `* reverted:` marker in README."); got != "decision" {
		t.Fatalf("inferRepoKind(backticked marker) = %q, want decision", got)
	}
	if got := inferRepoKind(`They asked about "откатили обратно" in docs.`); got != "decision" {
		t.Fatalf("inferRepoKind(quoted marker) = %q, want decision", got)
	}
	if got := inferRepoKind("We reverted the pooling patch after timeouts."); got != "rejected" {
		t.Fatalf("inferRepoKind(unquoted reversal) = %q, want rejected", got)
	}
	if got := inferRepoKind("Never run migrations without a lock timeout."); got != "gotcha" {
		t.Fatalf("inferRepoKind(gotcha) = %q, want gotcha", got)
	}
}

func TestOrientProjectFixesSkipsViewerPairs(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	pairs := []index.FixPair{
		{
			Sig:     1,
			Failed:  "$ rg -n connection internal/",
			Error:   "dial tcp 127.0.0.1:5432: connect: connection refused",
			Command: "$ docker compose up -d postgres",
			Project: "app",
			When:    now,
		},
		{
			Sig:     2,
			Failed:  "$ go test ./internal/checkout/...",
			Error:   "dial tcp 127.0.0.1:5432: connect: connection refused",
			Command: "$ docker compose up -d postgres",
			Project: "app",
			When:    now.Add(-time.Minute),
		},
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(pairs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixes.gob"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	got := orientProjectFixes(dir, []string{"app"}, "/work/app", "")
	if len(got) != 1 || !strings.Contains(got[0], "docker compose up -d postgres") {
		t.Fatalf("orientProjectFixes = %v, want only docker compose remedy pair", got)
	}
}

func TestResolveRecallCausalPathAndBriefHelpers(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	realRepo := filepath.Join(tmp, "work", "app")
	pkgDir := filepath.Join(realRepo, "pkg")
	if err := os.MkdirAll(filepath.Join(realRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workerFile := filepath.Join(pkgDir, "worker.go")
	if err := os.WriteFile(workerFile, []byte("package pkg\n\nfunc RunWorker() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(tmp, "outside.go")
	if err := os.WriteFile(outsideFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := resolveRecallCausalPath("", "pkg/worker.go"); got != "" {
		t.Fatalf("resolveRecallCausalPath(empty cwd) = %q, want empty", got)
	}
	scratch := filepath.Join(tmp, ".gemini", "antigravity", "brain", "conv-1", "scratch", "notes.md")
	if got := resolveRecallCausalPath(realRepo, scratch); got != "" {
		t.Fatalf("resolveRecallCausalPath(scratch) = %q, want empty", got)
	}
	if got := resolveRecallCausalPath(realRepo, "pkg/worker.go:42"); got != workerFile {
		t.Fatalf("resolveRecallCausalPath(rel:line) = %q, want %q", got, workerFile)
	}
	if got := resolveRecallCausalPath(realRepo, "worker.go"); got != workerFile {
		t.Fatalf("resolveRecallCausalPath(basename) = %q, want %q", got, workerFile)
	}
	if got := resolveRecallCausalPath(realRepo, workerFile); got != workerFile {
		t.Fatalf("resolveRecallCausalPath(abs inside) = %q, want %q", got, workerFile)
	}
	if got := resolveRecallCausalPath(realRepo, outsideFile); got != "" {
		t.Fatalf("resolveRecallCausalPath(abs outside) = %q, want empty", got)
	}

	symRepo := filepath.Join(tmp, "work", "app-link")
	if err := os.Symlink(realRepo, symRepo); err != nil {
		t.Fatal(err)
	}
	if !recallPathWithinRoot(symRepo, workerFile) {
		t.Fatal("recallPathWithinRoot(symlink, realTarget) = false, want true")
	}
	if recallPathWithinRoot(symRepo, outsideFile) {
		t.Fatal("recallPathWithinRoot(symlink, outside) = true, want false")
	}

	if got := capRecallCausalLine("deja: привет мир", 0); got != "" {
		t.Fatalf("capRecallCausalLine(maxBytes=0) = %q, want empty", got)
	}
	capped := capRecallCausalLine("deja: привет мир из воркера", 14)
	if len(capped) > 14 || !utf8.ValidString(capped) || strings.HasPrefix(capped, "deja:") {
		t.Fatalf("capRecallCausalLine = %q (len=%d, validUTF8=%v)", capped, len(capped), utf8.ValidString(capped))
	}

	if !sameRecallBriefLine("a — same summary", "b — same summary extended") {
		t.Fatal("expected sameRecallBriefLine to match shared tail prefix")
	}
	if sameRecallBriefLine("a — first topic", "b — second topic") {
		t.Fatal("did not expect sameRecallBriefLine on distinct tails")
	}

	for i, sid := range []string{"w1", "w2"} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "app", sid+".jsonl"), sid, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-09-0%dT10:00:00Z","message":{"role":"user","content":"update pkg/worker.go retry backoff"}}`, sid, realRepo, i+1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-09-0%dT10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e%d","name":"Edit","input":{"file_path":%q,"old_string":"RunWorker()","new_string":"RunWorker()"}},{"type":"text","text":"Decision: keep pkg/worker.go backoff bounded to 30s."}]}}`, sid, realRepo, i+1, i+1, workerFile),
		})
	}
	if err := index.Ensure(idxDir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if brief := liveRecallBriefScoped(idxDir, realRepo, "app", 1, "pkg/worker.go"); brief != "" {
		t.Fatalf("liveRecallBriefScoped(offset=1) = %q, want empty", brief)
	}
	brief := liveRecallBriefScoped(idxDir, realRepo, "app", 0, "pkg/worker.go")
	if !strings.HasPrefix(brief, "live repo brief: ") || !strings.Contains(brief, "worker.go") {
		t.Fatalf("liveRecallBriefScoped(offset=0) = %q, want live repo brief mentioning worker.go", brief)
	}
}
