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
)

func TestHandoffSkillFilesAndFrontmatter(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "skills", "deja-handoff", "SKILL.md"),
		filepath.Join("..", "..", "claude-plugin", "skills", "deja-handoff", "SKILL.md"),
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		text := string(raw)
		for _, want := range []string{
			"name: deja-handoff",
			`mode: "handoff"`,
			"deja handoff",
			"--to <agent>",
			"--project <name>",
			"--harness <name>",
			"--exec",
			"deja last",
			"deja status",
			"deja install --auto",
			"npx -y @vshulcz/deja-vu handoff",
			"User said (this project)",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", p, want)
			}
		}
		if strings.HasSuffix(filepath.ToSlash(p), "skills/deja-handoff/SKILL.md") &&
			!strings.Contains(filepath.ToSlash(p), "claude-plugin/") {
			if !strings.Contains(text, "openclaw:") {
				t.Errorf("%s should include openclaw registry metadata", p)
			}
		}
		if strings.Contains(filepath.ToSlash(p), "claude-plugin/") && strings.Contains(text, "\nmetadata:") {
			t.Errorf("%s must keep flat frontmatter without metadata", p)
		}
	}
}

func TestSearchSkillStandaloneScopingAndTrail(t *testing.T) {
	bundled, err := os.ReadFile(filepath.Join("..", "..", "skills", "deja-search", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	generated := cliSkillRegistryFile()
	if string(bundled) != generated {
		t.Fatalf("skills/deja-search/SKILL.md drifted from cliSkillRegistryFile()")
	}
	for _, want := range []string{
		"When running without hooks or MCP",
		"deja recall",
		"deja --framed fix",
		"deja --framed blame",
		"deja --framed trail",
		"deja status",
		"deja install --auto",
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("deja-search skill missing %q", want)
		}
	}
}

func TestHandoffProjectAndHarnessFlags(t *testing.T) {
	withStatsStores(t)

	out, err := captureRun(t, "handoff", "--project", "alpha", "--harness", "claude")
	if err != nil {
		t.Fatalf("handoff --project alpha --harness claude: %v", err)
	}
	if !strings.Contains(out, "march alpha") {
		t.Fatalf("expected newest alpha claude session c2, got:\n%s", out)
	}

	out, err = captureRun(t, "handoff", "--project", "gamma", "--harness", "codex")
	if err != nil {
		t.Fatalf("handoff --project gamma --harness codex: %v", err)
	}
	if !strings.Contains(out, "gamma") {
		t.Fatalf("expected codex session codex1, got:\n%s", out)
	}
	if _, cerr := captureRun(t, "handoff", "--harness", "codex"); cerr == nil || !strings.Contains(cerr.Error(), "no codex session indexed for this project") {
		t.Fatalf("expected project-scoped missing codex error, got %v", cerr)
	}

	out, err = captureRun(t, "handoff", "--project", "beta", "--to", "codex")
	if err != nil {
		t.Fatalf("handoff --project beta --to codex: %v", err)
	}
	if !strings.Contains(out, "long beta session") {
		t.Fatalf("expected beta session c3, got:\n%s", out)
	}

	for _, bad := range [][]string{
		{"--project"},
		{"--harness"},
		{"--harness", "not-a-real-harness"},
	} {
		if err := runHandoff(index.DefaultDir(), bad, discardWriter{}); err == nil {
			t.Errorf("runHandoff(%v) unexpectedly succeeded", bad)
		}
	}
}

func TestStatusCommandReadOnlyAndWiring(t *testing.T) {
	hermeticEnv(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, k := range []string{
		"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT",
		"CODEX_THREAD_ID", "CODEX_CI", "CODEX_SANDBOX", "CODEX_MANAGED_BY_NPM",
		"GEMINI_CLI", "OPENCODE", "CURSOR_AGENT",
		"ANTIGRAVITY_AGENT", "KIMI_CLI", "QWEN_CODE",
	} {
		t.Setenv(k, "")
	}

	missingIdx := filepath.Join(t.TempDir(), "never-created-index")
	var buf bytes.Buffer
	err = runStatus(missingIdx, []string{"--json"}, &buf)
	if err != nil {
		t.Fatalf("runStatus unwired: %v", err)
	}
	if _, statErr := os.Stat(missingIdx); !os.IsNotExist(statErr) {
		t.Fatalf("runStatus must not create missing index dir %s (stat err=%v)", missingIdx, statErr)
	}

	var rep statusReport
	if jsonErr := json.Unmarshal(buf.Bytes(), &rep); jsonErr != nil {
		t.Fatalf("unmarshal status JSON: %v\n%s", jsonErr, buf.String())
	}
	if rep.HooksWired || rep.MCPWired || rep.IndexExists {
		t.Fatalf("expected unwired empty status, got %#v", rep)
	}
	if rep.InstallCommand != "deja install --auto" {
		t.Fatalf("InstallCommand = %q, want %q", rep.InstallCommand, "deja install --auto")
	}

	t.Setenv("CLAUDECODE", "1")
	buf.Reset()
	if err = runStatus(missingIdx, []string{"--json"}, &buf); err != nil {
		t.Fatalf("runStatus active harness: %v", err)
	}
	var repActive statusReport
	if err = json.Unmarshal(buf.Bytes(), &repActive); err != nil {
		t.Fatal(err)
	}
	if repActive.ActiveHarness != "claude" {
		t.Fatalf("ActiveHarness = %q, want claude", repActive.ActiveHarness)
	}

	claudeSettings := filepath.Join(home, ".claude", "settings.json")
	if err = os.MkdirAll(filepath.Dir(claudeSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"deja hook-context"}]}]}}`
	if err = os.WriteFile(claudeSettings, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	if err = runStatus(missingIdx, []string{"--harness", "claude", "--json"}, &buf); err != nil {
		t.Fatalf("runStatus wired: %v", err)
	}
	var repWired statusReport
	if err = json.Unmarshal(buf.Bytes(), &repWired); err != nil {
		t.Fatal(err)
	}
	if !repWired.HooksWired || repWired.InstallCommand != "" {
		t.Fatalf("expected wired claude hooks and empty InstallCommand, got %#v", repWired)
	}
}

func TestTrailChronologicalOrderAndJSON(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	target := filepath.Join(repo, "pkg", "auth", "token.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package auth\nfunc Parse() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-repo", "t-mid.jsonl"), "t-mid", []string{
		`{"type":"user","sessionId":"t-mid","timestamp":"2026-02-15T10:00:00Z","message":{"role":"user","content":"middle change to pkg/auth/token.go"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-repo", "t-new.jsonl"), "t-new", []string{
		`{"type":"user","sessionId":"t-new","timestamp":"2026-03-20T10:00:00Z","message":{"role":"user","content":"newest change to pkg/auth/token.go"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-repo", "t-old.jsonl"), "t-old", []string{
		`{"type":"user","sessionId":"t-old","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":"initial pkg/auth/token.go setup"}}`,
	})

	var buf bytes.Buffer
	dir := index.DefaultDir()
	if err := runTrail(dir, []string{"pkg/auth/token.go", "--all-projects", "--json"}, &buf); err != nil {
		t.Fatalf("runTrail --json: %v", err)
	}
	var rep trailReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal trail JSON: %v\n%s", err, buf.String())
	}
	if rep.Total != 3 || len(rep.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %#v", rep)
	}
	wantOrder := []string{"t-old", "t-mid", "t-new"}
	for i, wantID := range wantOrder {
		if rep.Entries[i].ID != wantID {
			t.Fatalf("entry[%d].ID = %q, want %q (all=%#v)", i, rep.Entries[i].ID, wantID, rep.Entries)
		}
	}

	buf.Reset()
	if err := runTrail(dir, []string{"pkg/auth/token.go", "--all-projects", "--limit", "2"}, &buf); err != nil {
		t.Fatalf("runTrail text: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(2 of 3, oldest first):") || strings.Contains(out, "t-old") || !strings.Contains(out, "t-mid") || !strings.Contains(out, "t-new") {
		t.Fatalf("unexpected --limit 2 text output:\n%s", out)
	}

	buf.Reset()
	if err := runTrail(dir, []string{"pkg/auth/missing.go", "--all-projects"}, &buf); err != nil {
		t.Fatalf("runTrail empty: %v", err)
	}
	if !strings.Contains(buf.String(), "trail: no sessions mention or touch pkg/auth/missing.go") {
		t.Fatalf("unexpected empty trail output:\n%s", buf.String())
	}
}

func TestStatusTextErrorsAndHarnesses(t *testing.T) {
	hermeticEnv(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	envKeys := []string{
		"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT",
		"CODEX_THREAD_ID", "CODEX_CI", "CODEX_SANDBOX", "CODEX_MANAGED_BY_NPM",
		"GEMINI_CLI", "OPENCODE", "CURSOR_AGENT",
		"ANTIGRAVITY_AGENT", "KIMI_CLI", "QWEN_CODE",
	}
	clearEnvs := func() {
		for _, k := range envKeys {
			t.Setenv(k, "")
		}
	}
	clearEnvs()

	missingIdx := filepath.Join(t.TempDir(), "missing-index")
	var buf bytes.Buffer
	if err := runStatus(missingIdx, nil, &buf); err != nil {
		t.Fatalf("runStatus text unwired: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "Hooks: not wired") ||
		!strings.Contains(out, "Index: not built yet") ||
		!strings.Contains(out, "deja install --auto") {
		t.Fatalf("unexpected unwired status text:\n%s", out)
	}

	t.Setenv("CLAUDECODE", "1")
	buf.Reset()
	if err := runStatus(missingIdx, nil, &buf); err != nil {
		t.Fatalf("runStatus text active harness: %v", err)
	}
	if !strings.Contains(buf.String(), "Active harness: claude") {
		t.Fatalf("missing active harness line:\n%s", buf.String())
	}

	cases := []struct {
		env  string
		want string
	}{
		{"CODEX_THREAD_ID", "codex"},
		{"GEMINI_CLI", "gemini"},
		{"OPENCODE", "opencode"},
		{"CURSOR_AGENT", "cursor"},
		{"ANTIGRAVITY_AGENT", "antigravity"},
		{"KIMI_CLI", "kimi"},
		{"QWEN_CODE", "qwen"},
	}
	for _, tc := range cases {
		clearEnvs()
		t.Setenv(tc.env, "1")
		if got := statusDetectActiveHarness(); got != tc.want {
			t.Errorf("statusDetectActiveHarness() with %s=1 = %q, want %q", tc.env, got, tc.want)
		}
	}
	clearEnvs()

	for _, tc := range []struct{ in, want string }{
		{"claude-code", "claude"},
		{"gemini-cli", "gemini"},
		{"cursor-cli", "cursor"},
	} {
		if got := statusNormalizeHarnessInput(tc.in); got != tc.want {
			t.Errorf("statusNormalizeHarnessInput(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"hello status"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, discardWriter{}); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}
	claudeSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudeSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeSettings, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"deja hook-context"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	if err := runStatus(dir, []string{"--harness", "claude-code"}, &buf); err != nil {
		t.Fatalf("runStatus wired text: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "Harness: claude") ||
		!strings.Contains(out, "Hooks: wired") ||
		!strings.Contains(out, "Wired targets: claude-code") ||
		!strings.Contains(out, "Index: 1 session(s)") {
		t.Fatalf("unexpected wired status text:\n%s", out)
	}

	for _, bad := range [][]string{
		{"--harness"},
		{"--harness", "bogus-harness"},
		{"--bogus"},
		{"positional"},
	} {
		if err := runStatus(missingIdx, bad, discardWriter{}); err == nil {
			t.Errorf("runStatus(%v) unexpectedly succeeded", bad)
		}
	}
}

func TestTrailParsingOrderingAndErrors(t *testing.T) {
	opts, err := parseTrailArgs([]string{"--project", "alpha", "--harness", "claude", "--limit", "5", "--", "-dash.go"})
	if err != nil {
		t.Fatalf("parseTrailArgs valid: %v", err)
	}
	if opts.project != "alpha" || opts.harness != "claude" || opts.limit != 5 || opts.path != "-dash.go" {
		t.Fatalf("unexpected parsed options: %#v", opts)
	}

	for _, bad := range [][]string{
		{},
		{"a.go", "b.go"},
		{"--", "   "},
		{"--project"},
		{"--harness"},
		{"--limit"},
		{"--limit", "0"},
		{"--limit", "201"},
		{"--bogus"},
	} {
		if _, perr := parseTrailArgs(bad); perr == nil {
			t.Errorf("parseTrailArgs(%v) unexpectedly succeeded", bad)
		}
	}

	if terr := runTrail(index.DefaultDir(), []string{"pkg/auth/token.go", "--harness", "bogus-harness"}, discardWriter{}); terr == nil {
		t.Fatal("runTrail with bogus --harness unexpectedly succeeded")
	}

	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	if trailSessionTime(model.Session{Updated: t2}) != t2 {
		t.Fatal("trailSessionTime should fall back to Updated when Started is zero")
	}
	if compareTrailChrono(
		model.Session{ID: "a", Harness: "claude", Started: t1, Updated: t1},
		model.Session{ID: "b", Harness: "claude", Started: t1, Updated: t2},
	) >= 0 {
		t.Fatal("compareTrailChrono should break equal Started ties by Updated")
	}
	if compareTrailChrono(
		model.Session{ID: "a", Harness: "claude", Started: t1, Updated: t1},
		model.Session{ID: "b", Harness: "claude", Started: t1, Updated: t1},
	) >= 0 {
		t.Fatal("compareTrailChrono should break equal Started/Updated ties by Key")
	}

	var buf bytes.Buffer
	err = printTrailReport(&buf, "pkg/x.go", 1, []trailEntry{{ID: "id1", Harness: "claude", Updated: t1}})
	if err != nil {
		t.Fatalf("printTrailReport: %v", err)
	}
	if !strings.Contains(buf.String(), "(untitled)") {
		t.Fatalf("expected (untitled) fallback, got:\n%s", buf.String())
	}
	buf.Reset()
	if err := printTrailReport(&buf, "pkg/x.go", 1, []trailEntry{{ID: "id2", Harness: "codex"}}); err != nil {
		t.Fatalf("printTrailReport zero time: %v", err)
	}
	if !strings.Contains(buf.String(), "  -  codex") {
		t.Fatalf("expected '-' stamp fallback, got:\n%s", buf.String())
	}
}

func TestHandoffSourceFilteredErrorsAndPrefix(t *testing.T) {
	withStatsStores(t)

	out, err := captureRun(t, "handoff", "--project", "alpha", "c2")
	if err != nil {
		t.Fatalf("handoff --project alpha c2: %v", err)
	}
	if !strings.Contains(out, "march alpha") {
		t.Fatalf("expected c2 session digest, got:\n%s", out)
	}

	for _, args := range [][]string{
		{"handoff", "--project", "beta", "c2"},
		{"handoff", "--project", "alpha", "zzzz"},
		{"handoff", "--project", "missing-proj", "--harness", "claude"},
		{"handoff", "--project", "missing-proj"},
	} {
		if _, err := captureRun(t, args...); err == nil {
			t.Errorf("captureRun(%v) unexpectedly succeeded", args)
		}
	}
}
