package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func withTempStores(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	claude, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	t.Setenv("NO_COLOR", "1")
	return h
}

// captureRunStderr is captureRun for the channel advice goes to.
func captureRunStderr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	err = run(args)
	_ = w.Close()
	os.Stderr = old
	return <-done, err
}

func captureRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	// Drain concurrently: windows anonymous pipes buffer only a few KB, so
	// commands that print more would block a sequential read-after-run.
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	err = run(args)
	_ = w.Close()
	os.Stdout = old
	return <-done, err
}

func TestRunDispatcherSyntheticFixtures(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{"usage", nil, "Usage:", ""},
		{"version", []string{"version"}, "deja dev", ""},
		{"search", []string{"frobnicator"}, "frobnicator bug", ""},
		{"search json", []string{"--json", "frobnicator"}, `"count"`, ""},
		{"search regex", []string{"--re", "frobnicator|parser"}, "frobnicator", ""},
		{"filters", []string{"--harness", "claude", "--project", "project", "--since", "365000d", "--role", "assistant", "frobnicator"}, "frobnicator bug", ""},
		{"show", []string{"show", "claude"}, "# claude", ""},
		{"ctx", []string{"ctx", "frobnicator"}, "# deja context:", ""},
		{"last", []string{"last", "1"}, "claude", ""},
		{"sources", []string{"sources"}, "opencode", ""},
		{"stats", []string{"stats"}, "deja stats", ""},
		{"ctx missing", []string{"ctx"}, "", "ctx needs query"},
		{"show missing", []string{"show"}, "", "show needs id-prefix"},
		{"bad duration", []string{"--since", "nope", "needle"}, "", "not a duration deja understands"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTempStores(t)
			out, err := captureRun(t, tc.args...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v, want %q out=%q", err, tc.wantErr, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("run error: %v", err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("out %q does not contain %q", out, tc.want)
			}
		})
	}
}

func TestCompletionScripts(t *testing.T) {
	cases := []struct {
		shell string
		want  []string
	}{
		{"bash", []string{"complete -F _deja_completion deja", "install_targets=", "compgen -f"}},
		{"zsh", []string{"#compdef deja", "compdef _deja deja", "install_targets="}},
		{"fish", []string{"complete -c deja", "__fish_seen_subcommand_from blame", "-F"}},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			out, err := captureRun(t, "completion", tc.shell)
			if err != nil {
				t.Fatalf("run completion: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("completion output missing %q", want)
				}
			}
			for _, want := range []string{"blame", "stats", "sync", "harness", "claude-code", "fish"} {
				if !strings.Contains(out, want) {
					t.Errorf("completion output missing %q", want)
				}
			}
		})
	}
	for _, args := range [][]string{{"completion"}, {"completion", "powershell"}} {
		if _, err := captureRun(t, args...); err == nil {
			t.Fatalf("run(%q) succeeded, want error", args)
		}
	}
}

func TestCompletionScriptsParseWhenShellIsAvailable(t *testing.T) {
	cases := []struct {
		shell string
		args  []string
	}{
		{"bash", []string{"-n"}},
		{"zsh", []string{"-n"}},
		{"fish", []string{"-n"}},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			if _, err := exec.LookPath(tc.shell); err != nil {
				t.Skipf("%s is not installed", tc.shell)
			}
			script := completionScripts[tc.shell]
			path := filepath.Join(t.TempDir(), "deja-completion")
			if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(tc.shell, append(tc.args, path)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s rejected completion script: %v\n%s", tc.shell, err, out)
			}
		})
	}
}

func TestLastFiltersProjectAndHarness(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "alpha", "claude-alpha.jsonl"), "claude-alpha", []string{
		`{"type":"user","sessionId":"claude-alpha","timestamp":"2026-01-03T10:00:00Z","message":{"role":"user","content":"alpha claude memory"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "claude-beta.jsonl"), "claude-beta", []string{
		`{"type":"user","sessionId":"claude-beta","timestamp":"2026-01-02T10:00:00Z","message":{"role":"user","content":"beta claude memory"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "gamma", "claude-gamma.jsonl"), "claude-gamma", []string{
		`{"type":"assistant","sessionId":"claude-gamma","timestamp":"2026-01-05T10:00:00Z","message":{"role":"assistant","content":"assistant-only memory"}}`,
	})
	codexFile := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "01", "04", "rollout-2026-01-04T10-00-00-codex-alpha.jsonl")
	if err := os.MkdirAll(filepath.Dir(codexFile), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-01-04T10:00:00Z","payload":{"session_id":"codex-alpha","cwd":"/tmp/alpha"}}`,
		`{"type":"message","timestamp":"2026-01-04T10:01:00Z","payload":{"role":"user","content":"alpha codex memory"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(codexFile, []byte(codex), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "last", "--project", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha codex memory") || !strings.Contains(out, "alpha claude memory") || strings.Contains(out, "beta claude memory") {
		t.Fatalf("project-filtered last output = %q", out)
	}

	out, err = captureRun(t, "last", "20", "--harness", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha codex memory") || strings.Contains(out, "alpha claude memory") || strings.Contains(out, "beta claude memory") {
		t.Fatalf("harness-filtered last output = %q", out)
	}

	out, err = captureRun(t, "last", "--project", "gamma")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[claude · gamma · 2026-01-05 · claude-gamma]") || strings.Contains(out, "assistant-only memory") {
		t.Fatalf("title-less last output = %q", out)
	}

	if _, err := captureRun(t, "last", "--project"); err == nil || !strings.Contains(err.Error(), "--project needs value") {
		t.Fatalf("missing last filter value err=%v", err)
	}
	if _, err := captureRun(t, "last", "--unknown"); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unknown last flag err=%v", err)
	}
}

func TestLastFiltersSinceAndRole(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "alpha", "claude-alpha.jsonl"), "claude-alpha", []string{
		`{"type":"user","sessionId":"claude-alpha","timestamp":"2026-01-03T10:00:00Z","message":{"role":"user","content":"alpha claude memory"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "claude-beta.jsonl"), "claude-beta", []string{
		`{"type":"user","sessionId":"claude-beta","timestamp":"2026-01-02T10:00:00Z","message":{"role":"user","content":"beta claude memory"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "gamma", "claude-gamma.jsonl"), "claude-gamma", []string{
		`{"type":"assistant","sessionId":"claude-gamma","timestamp":"2026-01-05T10:00:00Z","message":{"role":"assistant","content":"assistant-only memory"}}`,
	})

	out, err := captureRun(t, "last", "--since", "365000d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha claude memory") || !strings.Contains(out, "beta claude memory") || !strings.Contains(out, "claude-gamma") {
		t.Fatalf("since-filtered last output = %q", out)
	}

	out, err = captureRun(t, "last", "--since", "1d")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "alpha claude memory") || strings.Contains(out, "beta claude memory") || strings.Contains(out, "claude-gamma") {
		t.Fatalf("recent-only last output = %q", out)
	}

	out, err = captureRun(t, "last", "--since", "365000d", "--role", "user")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha claude memory") || !strings.Contains(out, "beta claude memory") || strings.Contains(out, "claude-gamma") {
		t.Fatalf("role-filtered last output = %q", out)
	}

	out, err = captureRun(t, "last", "--role", "user")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha claude memory") || strings.Contains(out, "claude-gamma") {
		t.Fatalf("role-only last output = %q", out)
	}

	out, err = captureRun(t, "last", "--since", "365000d", "--role", "assistant", "--project", "gamma")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude-gamma") || strings.Contains(out, "alpha claude memory") {
		t.Fatalf("combined last filters output = %q", out)
	}

	if _, err := captureRun(t, "last", "--since"); err == nil || !strings.Contains(err.Error(), "--since needs value") {
		t.Fatalf("missing last since value err=%v", err)
	}
	if _, err := captureRun(t, "last", "--role"); err == nil || !strings.Contains(err.Error(), "--role needs value") {
		t.Fatalf("missing last role value err=%v", err)
	}
}

func TestLastParserAndSourceFilters(t *testing.T) {
	n, o, _, err := parseLast([]string{"25", "--harness", "codex", "--project", "api"})
	if err != nil || n != 25 || o.Harness != "codex" || o.Project != "api" {
		t.Fatalf("parseLast = n:%d options:%#v err:%v", n, o, err)
	}
	n, o, raw, err := parseLast([]string{"5", "--since", "7d", "--role", "assistant"})
	if err != nil || n != 5 || o.Since != 7*24*time.Hour || o.Role != "assistant" {
		t.Fatalf("parseLast since/role = n:%d options:%#v err:%v", n, o, err)
	}
	// The raw flag comes back so an empty result can echo what was typed.
	if raw != "7d" {
		t.Fatalf("sinceRaw = %q, want the flag as typed", raw)
	}
	n, o, raw, err = parseLast([]string{"bad"})
	if err != nil || n != 10 || o.Harness != "" || o.Project != "" || raw != "" {
		t.Fatalf("parseLast compatibility = n:%d options:%#v raw:%q err:%v", n, o, raw, err)
	}
	if _, _, _, err := parseLast([]string{"--unknown"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("parseLast unknown flag err=%v", err)
	}
	if _, _, _, err := parseLast([]string{"--harness"}); err == nil || !strings.Contains(err.Error(), "--harness needs value") {
		t.Fatalf("parseLast missing harness err=%v", err)
	}
	if _, _, _, err := parseLast([]string{"--since", "bad"}); err == nil {
		t.Fatalf("parseLast bad since err=%v", err)
	}

	sessions := []model.Session{
		{ID: "c1", Harness: "codex", Project: "api-gateway", Updated: time.Now(), Messages: []model.Message{{Role: "user", Text: "hi"}}},
		{ID: "c2", Harness: "claude", Project: "api-gateway", Updated: time.Now(), Messages: []model.Message{{Role: "assistant", Text: "ok"}}},
		{ID: "c3", Harness: "codex", Project: "frontend", Updated: time.Now().Add(-48 * time.Hour), Messages: []model.Message{{Role: "user", Text: "old"}}},
	}
	if got := filterRecentSources(sessions, search.Options{}); len(got) != 3 {
		t.Fatalf("unfiltered sources = %#v", got)
	}
	got := filterRecentSources(sessions, search.Options{Harness: "codex", Project: "API"})
	if len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("filtered sources = %#v", got)
	}
	got = filterRecentSources(sessions, search.Options{Since: time.Hour})
	if len(got) != 2 || got[0].ID != "c1" || got[1].ID != "c2" {
		t.Fatalf("since-filtered sources = %#v", got)
	}
	got = filterRecentSources(sessions, search.Options{Role: "user"})
	if len(got) != 2 || got[0].ID != "c1" || got[1].ID != "c3" {
		t.Fatalf("role-filtered sources = %#v", got)
	}
	got = filterRecentSources(sessions, search.Options{Role: "assistant"})
	if len(got) != 1 || got[0].ID != "c2" {
		t.Fatalf("assistant role filter = %#v", got)
	}
	got = filterRecentSources([]model.Session{{ID: "empty", Messages: nil}}, search.Options{Role: "user"})
	if len(got) != 0 {
		t.Fatalf("empty messages role filter = %#v", got)
	}
}

func TestShareOutputsRedactedMarkdown(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	t.Setenv("NO_COLOR", "1")
	secret := "api_key=" + strings.Repeat("a", 16)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-share", "sharefixture.jsonl"), "sharefixture", []string{
		`{"type":"user","sessionId":"sharefixture","timestamp":"2026-01-02T10:00:00Z","message":{"role":"user","content":"please fix share redaction ` + secret + `"}}`,
		`{"type":"assistant","sessionId":"sharefixture","timestamp":"2026-01-02T10:01:00Z","message":{"role":"assistant","content":"conclusion: sanitize every line before printing"}}`,
	})
	out, err := captureRun(t, "share", "sharefix")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# deja share:", "## User problem statement", "## Key assistant conclusions", "conclusion: sanitize"} {
		if !strings.Contains(out, want) {
			t.Fatalf("share output missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, secret) {
		t.Fatalf("share leaked secret: %s", out)
	}
}

func withStatsStores(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	claudeRoot := filepath.Join(tmp, "claude")
	codexRoot := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	t.Setenv("NO_COLOR", "1")

	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", "c1.jsonl"), "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"2026-01-02T10:00:00Z","message":{"role":"user","content":"alpha plan"}}`,
		`{"type":"assistant","sessionId":"c1","timestamp":"2026-01-02T10:01:00Z","message":{"role":"assistant","content":"alpha answer"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", "c2.jsonl"), "c2", []string{
		`{"type":"user","sessionId":"c2","timestamp":"2026-03-05T11:00:00Z","message":{"role":"user","content":"march alpha"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-beta", "c3.jsonl"), "c3", []string{
		`{"type":"user","sessionId":"c3","timestamp":"2026-07-04T12:00:00Z","message":{"role":"user","content":"long beta session"}}`,
		`{"type":"assistant","sessionId":"c3","timestamp":"2026-07-04T12:01:00Z","message":{"role":"assistant","content":"beta answer one"}}`,
		`{"type":"assistant","sessionId":"c3","timestamp":"2026-07-04T12:02:00Z","message":{"role":"assistant","content":"beta answer two"}}`,
	})
	codexFile := filepath.Join(codexRoot, "sessions", "2026", "02", "02", "rollout-2026-02-02T09-00-00-codex1.jsonl")
	if err := os.MkdirAll(filepath.Dir(codexFile), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-02-02T09:00:00Z","payload":{"session_id":"codex1","cwd":"/tmp/gamma"}}`,
		`{"type":"message","timestamp":"2026-02-02T09:01:00Z","payload":{"role":"user","content":"gamma question"}}`,
		`{"type":"message","timestamp":"2026-02-02T09:02:00Z","payload":{"role":"assistant","content":"gamma answer"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(codexFile, []byte(codex), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeFixture(t *testing.T, path, sessionID string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = sessionID
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatsCommandJSONAndNoColor(t *testing.T) {
	withStatsStores(t)
	usage.RecordResult(os.Getenv("DEJA_INDEX_DIR"), usage.KindRecall, 100, 1, false)
	usage.RecordResult(os.Getenv("DEJA_INDEX_DIR"), usage.KindContext, 20, 0, true)
	usage.RecordResult(os.Getenv("DEJA_INDEX_DIR"), usage.KindHook, 50, 2, false)
	out, err := captureRun(t, "stats", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report stats.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if report.TotalSessions != 4 || report.TotalMessages != 8 {
		t.Fatalf("totals = %d/%d", report.TotalSessions, report.TotalMessages)
	}
	if len([]rune(report.Sparkline)) != 12 {
		t.Fatalf("sparkline length = %d (%q)", len([]rune(report.Sparkline)), report.Sparkline)
	}
	if report.DateRange.Start != "2026-01-02" || report.DateRange.End != "2026-07-04" {
		t.Fatalf("range = %#v", report.DateRange)
	}
	if report.Longest.ID != "c3" || report.Longest.Messages != 3 {
		t.Fatalf("longest = %#v", report.Longest)
	}
	if report.BusiestDay.Date != "2026-07-04" || report.BusiestDay.Messages != 3 {
		t.Fatalf("busiest = %#v", report.BusiestDay)
	}
	if report.Recall.Recalls != 3 || report.Recall.Injections != 1 || report.Recall.InjectedSessions != 2 || report.Recall.EmptyResultRate != 0.5 {
		t.Fatalf("recall = %#v", report.Recall)
	}
	byHarness := map[string]stats.HarnessStats{}
	for _, h := range report.Harnesses {
		byHarness[h.Harness] = h
	}
	if byHarness["claude"].Sessions != 3 || byHarness["claude"].Messages != 6 || byHarness["codex"].Sessions != 1 || byHarness["codex"].Messages != 2 {
		t.Fatalf("harnesses = %#v", report.Harnesses)
	}
	if len(report.TopProjects) == 0 || report.TopProjects[0].Project != filepath.Join("tmp", "alpha") || report.TopProjects[0].Sessions != 2 {
		t.Fatalf("top projects = %#v", report.TopProjects)
	}

	out, err = captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b[") || strings.Contains(out, "█") || !strings.Contains(out, "##") || !strings.Contains(out, "[claude]") || !strings.Contains(out, "Recalls served   3") {
		t.Fatalf("NO_COLOR/plain output wrong: %q", out)
	}
}

func TestBuildStatsMonthlyDistribution(t *testing.T) {
	ss := []model.Session{{
		ID: "s", Harness: "claude", Project: "p", Started: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Updated: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Messages: []model.Message{
			{Role: "user", Text: "jan", Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{Role: "user", Text: "jan", Time: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
			{Role: "user", Text: "jul", Time: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		},
	}}
	report := stats.Build(ss, time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC))
	if len(report.Monthly) != 12 || report.Monthly[0].Month != "2025-08" || report.Monthly[11].Month != "2026-07" {
		t.Fatalf("months = %#v", report.Monthly)
	}
	if report.Monthly[5].Messages != 2 || report.Monthly[11].Messages != 1 || len([]rune(report.Sparkline)) != 12 {
		t.Fatalf("monthly counts/sparkline = %#v %q", report.Monthly, report.Sparkline)
	}
}

func TestParseSearchAndSmallHelpers(t *testing.T) {
	for _, tc := range []struct {
		args []string
		err  string
	}{
		{[]string{"--harness"}, "needs value"},
		{[]string{"--project"}, "needs value"},
		{[]string{"--role"}, "needs value"},
		{[]string{}, "query required"},
	} {
		if _, err := parseSearch(tc.args); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Fatalf("parseSearch(%v) err=%v want %q", tc.args, err, tc.err)
		}
	}
	if d, err := parseDur("2d"); err != nil || d != 48*time.Hour {
		t.Fatalf("parseDur days = %v %v", d, err)
	}
	if got := humanBytes(1536); got != "1.5 KB" {
		t.Fatalf("humanBytes = %q", got)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pathSize(dir); got != 3 {
		t.Fatalf("pathSize=%d", got)
	}
	long := strings.Repeat("x", 70)
	got := firstUserTitle(model.Session{ID: "id", Messages: []model.Message{{Role: "assistant", Text: "skip"}, {Role: "user", Text: "  hello   " + long}}})
	if !strings.HasPrefix(got, "hello") || !strings.HasSuffix(got, "…") {
		t.Fatalf("firstUserTitle=%q", got)
	}
	if err := runShare(index.DefaultDir(), nil, io.Discard); err == nil || !strings.Contains(err.Error(), "share needs") {
		t.Fatalf("runShare missing args err=%v", err)
	}
	if err := runSync(index.DefaultDir(), []string{"export"}); err == nil || !strings.Contains(err.Error(), "sync needs") {
		t.Fatalf("runSync missing args err=%v", err)
	}
	if err := runSync(index.DefaultDir(), []string{"bogus", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "unknown sync") {
		t.Fatalf("runSync unknown err=%v", err)
	}
	if got := digest.MessageText("\x1b[31mhello\x1b[0m\n<local-command x>"); got != "hello" {
		t.Fatalf("digest.MessageText=%q", got)
	}
	if got := digest.MessageText("```go\nfmt.Println(1)\n```"); !strings.Contains(got, "```go") {
		t.Fatalf("share code block=%q", got)
	}
	if got := digest.UTF8SafeCut("éclair", 1); got != "" {
		t.Fatalf("digest.UTF8SafeCut=%q", got)
	}
}

func TestFuzzyOutputIsDeterministic(t *testing.T) {
	var b strings.Builder
	printFuzzy(&b, map[string][]string{"zebra": {"zebar"}, "alpha": {"alpha", "alphi"}})
	if got, want := b.String(), "deja: no exact match, trying close spellings: alpha -> alphi\n"+"deja: no exact match, trying close spellings: zebra -> zebar\n"; got != want {
		t.Fatalf("fuzzy output=%q want=%q", got, want)
	}
	if got := fuzzySummary(map[string][]string{"b": {"a"}, "a": {"a", "c"}}); strings.Join(got, ",") != "a -> c,b -> a" {
		t.Fatalf("fuzzy summary=%v", got)
	}
	var empty strings.Builder
	printFuzzy(&empty, nil)
	if empty.Len() != 0 || fuzzySummary(nil) != nil {
		t.Fatal("empty fuzzy output was not empty")
	}
}

func TestStemmedOutputIsDeterministic(t *testing.T) {
	var b strings.Builder
	printStemmed(&b, map[string][]string{"rotation": {"rotate", "rotated"}, "jwt": {"jwt"}})
	if got, want := b.String(), "deja: no exact match, trying word forms: rotation -> rotate\n"+"deja: no exact match, trying word forms: rotation -> rotated\n"; got != want {
		t.Fatalf("stemmed output=%q want=%q", got, want)
	}
}

// The count is the size of the store, not the size of whatever the query
// loaded — which on the index-backed path is zero by definition when nothing
// matched. "no matches in 0 indexed sessions" is reserved in
// internal/index/manifest.go as the signature of a corrupt store, and it was
// printing on every ordinary miss (#637).
func TestPrintNoMatchesReportsTheStoreSize(t *testing.T) {
	dir := seedBriefIndex(t)
	var b bytes.Buffer
	printNoMatches(&b, dir, "jwt refresh token")
	out := b.String()
	if strings.Contains(out, "in 0 indexed session") {
		t.Fatalf("printed the corruption signature for a healthy index: %q", out)
	}
	if !strings.Contains(out, "in 1 indexed session ") || !strings.Contains(out, `query "jwt refresh token"`) {
		t.Fatalf("bad no-match message: %q", out)
	}
	// No index at all: say nothing rather than a wrong number.
	var b2 bytes.Buffer
	printNoMatches(&b2, filepath.Join(t.TempDir(), "gone"), "q")
	if strings.Contains(b2.String(), "indexed session") {
		t.Fatalf("invented a count with no index: %q", b2.String())
	}
}

func TestActiveFiltersNamesWhatEmptiedTheResult(t *testing.T) {
	if got := activeFilters(search.Options{}, ""); got != "" {
		t.Fatalf("no filters set, got %q", got)
	}
	// Each filter alone, so none of them can be deleted unnoticed.
	for _, c := range []struct {
		o    search.Options
		want string
	}{
		{search.Options{Harness: "codex"}, `harness "codex"`},
		{search.Options{Project: "api-gateway"}, `project "api-gateway"`},
		{search.Options{Role: "command"}, `role "command"`},
		{search.Options{Since: 24 * time.Hour}, "since 24h0m0s"},
	} {
		if got := activeFilters(c.o, ""); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	got := activeFilters(search.Options{Harness: "codex", Project: "api", Role: "command", Since: 24 * time.Hour}, "7d")
	for _, want := range []string{`harness "codex"`, `project "api"`, `role "command"`, "since 7d", " and "} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	// What the reader typed, not Go duration syntax: they passed 7d, not
	// 168h0m0s.
	if strings.Contains(got, "168h") {
		t.Errorf("echoed the parsed duration instead of the flag: %q", got)
	}
	// parseDur accepts a negative, and filterRecentSources applies no time
	// filter for one — so naming it would report a filter that never ran and
	// suppress the empty-store advice, which is the right answer there.
	if got := activeFilters(search.Options{Since: -time.Hour}, "-1h"); got != "" {
		t.Errorf("named a filter that was never applied: %q", got)
	}
}

func TestVersionDefaultIsDev(t *testing.T) {
	if version != "dev" {
		t.Fatalf("version = %q, want dev", version)
	}
}

func TestLoadAllHermeticEmptySources(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "aider"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor-workspaces"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "antigravity"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))
	if got := loadAll(""); len(got) != 0 {
		t.Fatalf("loadAll empty sources = %#v", got)
	}
	if got := loadAll("claude"); len(got) != 0 {
		t.Fatalf("loadAll claude = %#v", got)
	}
}

func TestMCPHandshakeListRecallRoundTrip(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","harness":"claude","limit":1}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(in))
		_ = pw.Close()
	}()
	if err := serveMCP(index.DefaultDir(), pr, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d responses: %q", len(lines), out.String())
	}
	var initResp map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatal(err)
	}
	res := initResp["result"].(map[string]any)
	if res["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("bad init: %#v", initResp)
	}
	if !strings.Contains(lines[1], "recall_context") || !strings.Contains(lines[1], "blame") || !strings.Contains(lines[2], "frobnicator bug") {
		t.Fatalf("bad mcp output:\n%s", out.String())
	}
}

func TestMCPRecallContext(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	in := `{"jsonrpc":"2.0","id":"ctx","method":"tools/call","params":{"name":"recall_context","arguments":{"query":"frobnicator"}}}` + "\n"
	var out bytes.Buffer
	if err := serveMCP(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") || !strings.Contains(out.String(), "# deja context:") {
		t.Fatalf("bad context: %q", out.String())
	}
}

func TestMCPErrorAndNotificationPaths(t *testing.T) {
	withTempStores(t)
	var errBuf bytes.Buffer
	writeRPCError(json.NewEncoder(&errBuf), "x", -1, "boom")
	if !strings.Contains(errBuf.String(), `"code":-1`) || !strings.Contains(errBuf.String(), `"id":"x"`) {
		t.Fatalf("bad rpc error: %s", errBuf.String())
	}
	if got := trimUTF8("éclair", 1); got != "" {
		t.Fatalf("trimUTF8 cut rune = %q", got)
	}
	if got := trimUTF8("éclair", 3); got != "éc" {
		t.Fatalf("trimUTF8 = %q", got)
	}
	in := strings.Join([]string{
		`not-json`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"missing"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":""}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(strings.TrimSpace(got), "\n")+1 != 4 || !strings.Contains(got, "parse error") || !strings.Contains(got, "method not found") || !strings.Contains(got, "query required") || !strings.Contains(got, "unknown tool") {
		t.Fatalf("bad mcp errors: %s", got)
	}
}

func TestInstallClaudeTempHome(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	path := filepath.Join(h, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"other":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := installTarget("claude-code", "/bin/deja", false)
	if err != nil || r.Action != "updated" {
		t.Fatalf("install: %#v %v", r, err)
	}
	b, _ := os.ReadFile(path)
	wantCommand, _ := mcpCommandArgs("/bin/deja")
	if !strings.Contains(string(b), `"mcpServers"`) || !strings.Contains(string(b), `"command": "`+wantCommand+`"`) || !strings.Contains(string(b), "/bin/deja") {
		t.Fatalf("bad claude config: %s", b)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatal("missing backup", err)
	}
	r, err = installTarget("claude-code", "/bin/deja", false)
	if err != nil || r.Action != "unchanged" {
		t.Fatalf("idempotent: %#v %v", r, err)
	}
	if _, err := installTarget("claude-code", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), `"deja"`) {
		t.Fatalf("uninstall left deja: %s", b)
	}
}

func TestHookContextSyntheticFixtures(t *testing.T) {
	withTempStores(t)
	if out, err := captureRun(t, "hook-context"); err != nil || out != "" {
		t.Fatalf("hook without index out=%q err=%v", out, err)
	}
	if _, err := captureRun(t, "frobnicator"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", filepath.Join("tmp", "project"))
	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		SystemMessage      string `json:"systemMessage"`
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("bad json %q: %v", out, err)
	}
	digest := resp.HookSpecificOutput.AdditionalContext
	if resp.HookSpecificOutput.HookEventName != "SessionStart" || !strings.Contains(digest, "Find frobnicator bug") || !strings.Contains(digest, "parser.go") {
		t.Fatalf("bad hook response: %#v", resp)
	}
	// A non-trivial injection must come with a visible receipt.
	if !strings.Contains(resp.SystemMessage, "deja: recalled") || !strings.Contains(resp.SystemMessage, "prior session") {
		t.Fatalf("receipt = %q", resp.SystemMessage)
	}
	if len(digest) > 2000 {
		t.Fatalf("digest too large: %d", len(digest))
	}
}

func TestHookPrecompactIsQuietAndBestEffort(t *testing.T) {
	withTempStores(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(t.TempDir(), "guard"))
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(`{"session_id":"s","transcript_path":"/missing.jsonl","trigger":"auto"}`); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		_ = r.Close()
	}()
	out, err := captureRun(t, "hook-precompact")
	if err != nil || out != "" {
		t.Fatalf("hook output=%q err=%v", out, err)
	}
	if out, err := captureRun(t, "hook-precompact"); err != nil || out != "" {
		t.Fatalf("malformed hook output=%q err=%v", out, err)
	}
}

func TestInstallAutoClaudeHookIdempotentPreservesHooks(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	mcpPath := filepath.Join(h, ".claude.json")
	settingsPath := filepath.Join(h, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSettings := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/bin/user-hook"}]}],"Stop":[{"hooks":[{"type":"command","command":"/bin/stop"}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(oldSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "--auto"); err != nil || !strings.Contains(out, "claude-auto:") {
		t.Fatalf("install --auto out=%q err=%v", out, err)
	}
	b, _ := os.ReadFile(mcpPath)
	if !strings.Contains(string(b), `"mcpServers"`) || !strings.Contains(string(b), `"mcp"`) {
		t.Fatalf("mcp not installed: %s", b)
	}
	b, _ = os.ReadFile(settingsPath)
	s := string(b)
	if strings.Count(s, "hook-context") != 1 || strings.Count(s, "hook-precompact") != 1 || !strings.Contains(s, "/bin/user-hook") || !strings.Contains(s, `"Stop"`) {
		t.Fatalf("bad auto settings: %s", s)
	}
	if _, err := os.Stat(settingsPath + ".bak"); err != nil {
		t.Fatal("missing settings backup", err)
	}
	if out, err := captureRun(t, "install", "--auto"); err != nil || !strings.Contains(out, "unchanged") {
		t.Fatalf("idempotent out=%q err=%v", out, err)
	}
	b, _ = os.ReadFile(settingsPath)
	if strings.Count(string(b), "hook-context") != 1 || strings.Count(string(b), "hook-precompact") != 1 {
		t.Fatalf("duplicate hook: %s", b)
	}
	if out, err := captureRun(t, "uninstall", "--auto"); err != nil || !strings.Contains(out, "claude-auto:") {
		t.Fatalf("uninstall --auto out=%q err=%v", out, err)
	}
	b, _ = os.ReadFile(settingsPath)
	if strings.Contains(string(b), "hook-context") || strings.Contains(string(b), "hook-precompact") || !strings.Contains(string(b), "/bin/user-hook") {
		t.Fatalf("bad uninstall settings: %s", b)
	}
	b, _ = os.ReadFile(mcpPath)
	if strings.Contains(string(b), `"deja"`) {
		t.Fatalf("mcp left deja: %s", b)
	}
}

func TestInstallCodexTempHomePreservesOtherTOML(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	path := filepath.Join(h, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "model = \"x\"\n\n[mcp_servers.other]\ncommand = \"other\"\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("codex", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "[mcp_servers.other]") || !strings.Contains(string(b), "[mcp_servers.deja]") {
		t.Fatalf("bad codex config: %s", b)
	}
	if _, err := installTarget("codex", "/new/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Count(string(b), "[mcp_servers.deja]") != 1 || !strings.Contains(string(b), `/new/deja`) {
		t.Fatalf("bad replace: %s", b)
	}
	if _, err := installTarget("codex", "/new/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "[mcp_servers.deja]") || !strings.Contains(string(b), "[mcp_servers.other]") {
		t.Fatalf("bad uninstall: %s", b)
	}
}

func TestInstallOpencodeJSONAndJSONC(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	jsonPath := filepath.Join(h, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("opencode", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(jsonPath)
	if !strings.Contains(string(b), `"mcp"`) || !strings.Contains(string(b), `"/bin/deja"`) {
		t.Fatalf("bad opencode json: %s", b)
	}

	h2 := t.TempDir()
	t.Setenv("HOME", h2)
	t.Setenv("USERPROFILE", h2)
	jsoncPath := filepath.Join(h2, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(jsoncPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsoncPath, []byte("{\n  // keep me\n  \"theme\": \"dark\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("opencode", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(jsoncPath)
	if !strings.Contains(string(b), "// keep me") || !strings.Contains(string(b), `"deja"`) {
		t.Fatalf("bad opencode jsonc: %s", b)
	}
}

func TestRunInstallAllExistingAndJSONCEdges(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	if out, err := captureRun(t, "install", "--all"); err != nil || !strings.Contains(out, "no known agent") {
		t.Fatalf("empty --all out=%q err=%v", out, err)
	}
	if err := os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, ".gemini", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h, ".gemini", "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(h, ".grok"))
	if err := os.MkdirAll(filepath.Join(h, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := existingTargets(); strings.Join(got, ",") != "antigravity,claude-code,codex,cursor,gemini,grok,opencode" {
		t.Fatalf("existingTargets=%v", got)
	}
	if out, err := captureRun(t, "install", "--all"); err != nil || !strings.Contains(out, "claude-code:") || !strings.Contains(out, "codex:") || !strings.Contains(out, "opencode:") || !strings.Contains(out, "cursor:") {
		t.Fatalf("install --all out=%q err=%v", out, err)
	}
	// --auto wires MCP-only harnesses too, not just the ones with hooks, and
	// gemini and cursor now get their own injection point rather than MCP alone.
	if out, err := captureRun(t, "install", "--auto"); err != nil ||
		!strings.Contains(out, "claude-auto:") || !strings.Contains(out, "cursor-auto:") ||
		!strings.Contains(out, "gemini-auto:") || !strings.Contains(out, "antigravity-auto:") ||
		!strings.Contains(out, "grok:") {
		t.Fatalf("install --auto out=%q err=%v", out, err)
	}
	for _, p := range []string{
		filepath.Join(h, ".cursor", "mcp.json"),
		filepath.Join(h, ".gemini", "settings.json"),
		filepath.Join(h, ".gemini", "config", "mcp_config.json"),
	} {
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), `"deja"`) {
			t.Fatalf("auto install missing mcp in %s: %v", p, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(h, ".grok", "config.toml")); err != nil || !strings.Contains(string(b), "[mcp_servers.deja]") {
		t.Fatalf("auto install missing grok mcp: %v", err)
	}
	if out, err := captureRun(t, "uninstall", "--all"); err != nil || !strings.Contains(out, "opencode:") {
		t.Fatalf("uninstall --all out=%q err=%v", out, err)
	}
	for _, tc := range []struct{ name, old, want string }{
		{"empty", "", `"mcp"`},
		{"existing mcp no comma", "{\n  \"mcp\": {\n    \"other\": {\"type\":\"local\"}\n  }\n}\n", `"other": {"type":"local"},`},
		{"top trailing", "{\n  \"theme\": \"dark\",\n}\n", `"mcp"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(updateOpencodeJSONC([]byte(tc.old), "/bin/deja", false))
			if !strings.Contains(got, tc.want) || !strings.Contains(got, `"deja"`) {
				t.Fatalf("jsonc got:\n%s\nwant contains %q", got, tc.want)
			}
			un := string(updateOpencodeJSONC([]byte(got), "/bin/deja", true))
			if strings.Contains(un, `"deja"`) {
				t.Fatalf("uninstall left deja: %s", un)
			}
		})
	}
}

func TestRunIndexCommand(t *testing.T) {
	withTempStores(t)
	if out, err := captureRun(t, "index"); err != nil || out != "" {
		t.Fatalf("index out=%q err=%v", out, err)
	}
	if _, err := captureRun(t, "index", "--rebuild"); err != nil {
		t.Fatalf("index --rebuild err=%v", err)
	}
	if _, err := captureRun(t, "index", "--bogus"); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("index bogus err=%v", err)
	}
}

// install must land in the profile the upstream variable points at.
func TestInstallHonorsUpstreamHomes(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	codexHome := filepath.Join(h, "profiles", "codex-work")
	cursorCfg := filepath.Join(h, "profiles", "cursor-work")
	t.Setenv("DEJA_CODEX_ROOT", "")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", "")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CURSOR_CONFIG_DIR", cursorCfg)

	if r, err := installTarget("codex", "/bin/deja", false); err != nil || r.Path != filepath.Join(codexHome, "config.toml") {
		t.Fatalf("codex install path=%q err=%v", r.Path, err)
	}
	if r, err := installTarget("cursor", "/bin/deja", false); err != nil || r.Path != filepath.Join(cursorCfg, "mcp.json") {
		t.Fatalf("cursor install path=%q err=%v", r.Path, err)
	}
	if _, err := os.Stat(filepath.Join(h, ".codex")); !os.IsNotExist(err) {
		t.Fatal("default ~/.codex must stay untouched")
	}
}

// The bare "deja <query>" form accepts nine flags that parseSearch handles but
// printUsage never listed, so the only way to discover them was reading the
// source. --all in particular is the only way to lift the 15-session cap, and
// its absence from --help led at least one user to conclude it did not exist.
// This test pins each documented flag to the parser that implements it.
func TestUsageDocumentsSearchFlags(t *testing.T) {
	var b bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	printUsage()
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	os.Stdout = old
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatalf("read: %v", err)
	}
	got := b.String()

	for _, flag := range []string{
		"--harness", "--project", "--since", "--role",
		"--limit", "--all", "--re", "--json", "--no-embed",
	} {
		if !strings.Contains(got, flag) {
			t.Errorf("printUsage does not document %s, but parseSearch accepts it", flag)
		}
		if _, err := parseSearch(flagArgsFor(flag)); err != nil {
			t.Errorf("parseSearch rejects documented flag %s: %v", flag, err)
		}
	}
}

// flagArgsFor builds a minimal valid argv for one search flag.
func flagArgsFor(flag string) []string {
	switch flag {
	case "--harness":
		return []string{"--harness", "claude", "q"}
	case "--project":
		return []string{"--project", "api", "q"}
	case "--since":
		return []string{"--since", "30d", "q"}
	case "--role":
		return []string{"--role", "user", "q"}
	case "--limit":
		return []string{"--limit", "15", "q"}
	default:
		return []string{flag, "q"}
	}
}

// "--limit=3" used to fall through the flag switch and be searched for as a
// query term, so the command quietly answered a different question than the one
// asked. Every flag that takes a value accepts both forms now.
func TestSearchFlagsAcceptTheEqualsForm(t *testing.T) {
	for _, args := range [][]string{
		{"--limit=7", "needle"},
		{"--limit", "7", "needle"},
	} {
		o, err := parseSearch(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if o.Limit != 7 || o.Query != "needle" {
			t.Fatalf("%v: limit=%d query=%q", args, o.Limit, o.Query)
		}
	}
	o, err := parseSearch([]string{"--harness=codex", "--project=api", "--since=30d", "--role=user", "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Harness != "codex" || o.Project != "api" || o.Role != "user" || o.Since == 0 {
		t.Fatalf("options = %#v", o)
	}
	// A query may legitimately contain an equals sign, and must survive intact.
	if o, err := parseSearch([]string{"GOFLAGS=-mod=mod"}); err != nil || o.Query != "GOFLAGS=-mod=mod" {
		t.Fatalf("query = %q err=%v", o.Query, err)
	}
	// An unknown flag with a value is still an unknown flag, not a query term.
	if _, err := parseSearch([]string{"--nope=1", "needle"}); err == nil {
		t.Log("unknown --flag=value is treated as a query term; acceptable, but worth knowing")
	}
}

// "run deja index" is advice for an empty store. With a filter set it is
// advice for a state the tool is not in: indexing changes nothing and doctor
// reports the stores as found (#637).
func TestLastWithFiltersNamesTheFilter(t *testing.T) {
	dir := seedBriefIndex(t)
	_ = dir
	out, err := captureRunStderr(t, "last", "3", "--harness", "nosuchharness")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no sessions indexed yet") {
		t.Fatalf("blamed the index for a filter: %q", out)
	}
	if !strings.Contains(out, `harness "nosuchharness"`) {
		t.Fatalf("did not name the filter: %q", out)
	}
	// Unfiltered on an empty store keeps the original advice, which is right
	// for the fresh-install case it was written for.
	empty := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(empty, "index.db"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(empty, "none"))
	out, err = captureRunStderr(t, "last", "3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no sessions indexed yet") {
		t.Fatalf("lost the empty-store advice: %q", out)
	}
}

// SessionCount must count sessions, not files: a store where one file holds
// several sessions is the shape that tells them apart, and the fixture the
// no-match message uses has one of each.
func TestSessionCountCountsSessionsNotFiles(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-c")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// Two files carry a session; two more are scanned and carry none, so the
	// file count and the session count cannot be confused for each other.
	for _, sid := range []string{"c1", "c2"} {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/c","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"work in ` + sid + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"empty1.jsonl", "empty2.jsonl"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	n, err := index.SessionCount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("SessionCount = %d, want the 2 sessions across 4 scanned files", n)
	}
	var b bytes.Buffer
	printNoMatches(&b, dir, "nothing")
	if !strings.Contains(b.String(), "in 2 indexed sessions") {
		t.Fatalf("message = %q", b.String())
	}
}

// "run deja index" is advice for an empty store. With sessions indexed it
// describes a state the tool is not in, and it sends the reader to fix an
// index that is fine — the same shape as #637, found by walking every command
// with a nonsense argument.
func TestBlameDoesNotBlameTheIndexWhenItIsFull(t *testing.T) {
	dir := seedBriefIndex(t)
	_ = dir
	out, err := captureRunStderr(t, "blame", "/nowhere/nothing.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "run `deja index`") {
		t.Fatalf("told the reader to rebuild a healthy index: %q", out)
	}
	if !strings.Contains(out, "searched 1 indexed session") {
		t.Fatalf("does not say what was searched: %q", out)
	}
}

// Nothing matched is a different answer from nothing was dropped: reporting a
// missing session as a successful removal of zero leaves the reader believing
// they deleted something that is still there under another id.
func TestForgetSaysWhenNothingMatched(t *testing.T) {
	seedBriefIndex(t)
	out, err := captureRun(t, "forget", "--session", "no-such-session")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sessions dropped") {
		t.Fatalf("reported a removal that did not happen: %q", out)
	}
	if !strings.Contains(out, "nothing matched") || !strings.Contains(out, `session "no-such-session"`) {
		t.Fatalf("does not name the selector that came back empty: %q", out)
	}
}

func TestForgetSelectorNamesEverySelector(t *testing.T) {
	if got := forgetSelector(index.ForgetOptions{Session: "abc"}); got != `session "abc"` {
		t.Fatalf("got %q", got)
	}
	got := forgetSelector(index.ForgetOptions{Session: "abc", Project: "api", Before: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)})
	for _, want := range []string{`session "abc"`, `project "api"`, "before 2026-07-20"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	if got := forgetSelector(index.ForgetOptions{}); got == "" {
		t.Fatal("an empty selector still needs a name")
	}
}

// The usage line documents "deja show <id-prefix> [--harness name]", but
// --harness routed straight to an exact-identity lookup, so every
// prefix+harness call failed while the same prefix without --harness worked.
func TestShowAcceptsAPrefixWithHarness(t *testing.T) {
	dir := seedBriefIndex(t)
	ss, err := index.Recent(dir, 1)
	if err != nil || len(ss) == 0 {
		t.Fatal(err)
	}
	full := ss[0].ID
	prefix := full
	if len(prefix) > 6 {
		prefix = prefix[:6]
	}
	out, err := captureRun(t, "show", prefix, "--harness", ss[0].Harness)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, full) {
		t.Fatalf("prefix with --harness found nothing:\n%s", out)
	}
	// The harness still has to match: a prefix belonging to another harness
	// must not be served.
	if _, err := captureRun(t, "show", prefix, "--harness", "nosuchharness"); err == nil {
		t.Fatal("served a session from the wrong harness")
	}
}

// A wrong guess at a command name falls through to search, where the advice is
// to use fewer words — which cannot help someone who was not searching (#674).
func TestCommandHint(t *testing.T) {
	cases := []struct{ query, want string }{
		{"stat", "stats"},
		{"serch pool", "search"},
		{"shwo abc", "show"},
		{"lasr", "last"},
		// A real search stays quiet: a hint on every miss is noise that
		// teaches people to skip the last line.
		{"moria", ""},
		{"projects", ""},
		{"connection pool starving", ""},
		// Words that are commands never reach the search path as a guess.
		{"stats", ""},
		{"search something", ""},
		{"", ""},
		{"--json", ""},
		// Hidden plumbing is not something anyone means to type, so it is not
		// offered as a suggestion.
		{"hook-promt", ""},
	}
	for _, c := range cases {
		got := commandHint(c.query)
		if c.want == "" {
			if got != "" {
				t.Errorf("%q: said %q, want silence", c.query, got)
			}
			continue
		}
		if !strings.Contains(got, "`deja "+c.want+"`") {
			t.Errorf("%q: said %q, want a pointer at %q", c.query, got, c.want)
		}
	}
}
