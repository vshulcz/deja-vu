package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func withTempStores(t *testing.T) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	// Windows keeps notes under APPDATA, which HOME does not move: a note one
	// test wrote surfaced in the next test's `deja last`.
	t.Setenv("APPDATA", filepath.Join(h, "AppData", "Roaming"))
	claude, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	t.Setenv("NO_COLOR", "1")
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
		{"usage", nil, "deja help", ""},
		{"help", []string{"help"}, "Usage:", ""},
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
		{"show missing", []string{"show"}, "", "show needs an id prefix"},
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
		{"powershell", []string{"Register-ArgumentCompleter -Native -CommandName deja", "$installTargets =", "CompletionResult"}},
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
			for _, want := range []string{"blame", "stats", "sync", "harness", "claude-code", "fish", "powershell"} {
				if !strings.Contains(out, want) {
					t.Errorf("completion output missing %q", want)
				}
			}
		})
	}
	for _, args := range [][]string{{"completion"}, {"completion", "nu"}} {
		if _, err := captureRun(t, args...); err == nil {
			t.Fatalf("run(%q) succeeded, want error", args)
		}
	}
	// A second argument is named, not reported as a missing shell (#5077).
	if _, err := captureRun(t, "completion", "bash", "extra"); err == nil ||
		err.Error() != `completion takes one shell, got "bash" and "extra"` {
		t.Fatalf("run completion bash extra: %v", err)
	}
	if _, err := captureRun(t, "completion"); err == nil ||
		!strings.Contains(err.Error(), "completion needs bash, zsh, fish, or powershell") {
		t.Fatalf("run completion: %v", err)
	}
	powershell, err := captureRun(t, "completion", "powershell")
	if err != nil {
		t.Fatalf("run completion powershell: %v", err)
	}
	pwsh, err := captureRun(t, "completion", "pwsh")
	if err != nil {
		t.Fatalf("run completion pwsh: %v", err)
	}
	if powershell != pwsh {
		t.Error("pwsh alias returned a different completion script")
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
			cmd := exec.CommandContext(t.Context(), tc.shell, append(tc.args, path)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s rejected completion script: %v\n%s", tc.shell, err, out)
			}
		})
	}
}

func TestPowerShellCompletionScriptParsesWhenAvailable(t *testing.T) {
	tested := false
	for _, candidate := range []string{"pwsh", "powershell"} {
		shell, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		tested = true
		t.Run(candidate, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "deja-completion.ps1")
			if err := os.WriteFile(path, []byte(powershellCompletion), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), shell, "-NoProfile", "-NonInteractive", "-File", path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("PowerShell rejected completion script: %v\n%s", err, out)
			}
		})
	}
	if !tested {
		t.Skip("PowerShell is not installed")
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
	// No user turn: the assistant's first sentence is the title, because the
	// alternative is a row that says nothing at all (#692) — marked as the
	// agent's words since #1100, so it does not read as the reader's question.
	if !regexp.MustCompile(`\[claude\] gamma\s+Jan 5\s+claude-gamma\s+agent: assistant-only memory`).MatchString(out) {
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
	// The count is the only bare argument last takes. Ignoring anything else
	// left the reader with the default ten from every project and no sign that
	// the word they typed did nothing (#1618).
	if _, _, _, err := parseLast([]string{"bad"}); err == nil || !strings.Contains(err.Error(), "is not a count") {
		t.Fatalf("parseLast bad count err=%v", err)
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
	for _, want := range []string{"# deja share:", "## What was asked", "## What the assistant concluded", "conclusion: sanitize"} {
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
	// The notes file falls back to %AppData% on Windows, which moving HOME
	// does not move — so every test that left it unpinned shared one real
	// file, and a note another test promoted turned up in this fixture's
	// install proof and its totals (#2978).
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
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
	if err = json.Unmarshal([]byte(out), &report); err != nil {
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
	// Two agent recalls and one injection: recalls_served no longer carries
	// the injection it prints on the next line.
	if report.Recall.Recalls != 2 || report.Recall.Injections != 1 || report.Recall.InjectedSessions != 2 || report.Recall.EmptyResultRate != 0.5 {
		t.Fatalf("recall = %#v", report.Recall)
	}
	byHarness := map[string]stats.HarnessStats{}
	for _, h := range report.Harnesses {
		byHarness[h.Harness] = h
	}
	if byHarness["claude"].Sessions != 3 || byHarness["claude"].Messages != 6 || byHarness["codex"].Sessions != 1 || byHarness["codex"].Messages != 2 {
		t.Fatalf("harnesses = %#v", report.Harnesses)
	}
	if len(report.TopProjects) == 0 || report.TopProjects[0].Project != "tmp/alpha" || report.TopProjects[0].Sessions != 2 {
		t.Fatalf("top projects = %#v", report.TopProjects)
	}

	out, err = captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b[") || strings.Contains(out, "█") || !strings.Contains(out, "##") || !strings.Contains(out, "[claude]") || !strings.Contains(out, "Memory served    3") ||
		// The headline is about memory handed over at all, so it still sums
		// the two agent recalls and the one injection.
		!strings.Contains(out, "memory served 3 times") {
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
	if err := runSync(index.DefaultDir(), []string{"export"}); err == nil || !strings.Contains(err.Error(), "sync export needs a directory") {
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
	printNoMatches(&b, dir, "jwt refresh token", false)
	out := b.String()
	if strings.Contains(out, "in 0 indexed session") {
		t.Fatalf("printed the corruption signature for a healthy index: %q", out)
	}
	if !strings.Contains(out, "in 1 indexed session ") || !strings.Contains(out, `query "jwt refresh token"`) {
		t.Fatalf("bad no-match message: %q", out)
	}
	// No index at all: say nothing rather than a wrong number.
	var b2 bytes.Buffer
	printNoMatches(&b2, filepath.Join(t.TempDir(), "gone"), "q", false)
	if strings.Contains(b2.String(), "indexed session") {
		t.Fatalf("invented a count with no index: %q", b2.String())
	}
}

func TestActiveFiltersNamesWhatEmptiedTheResult(t *testing.T) {
	if got := activeFilters(search.Options{}, "", ""); got != "" {
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
		if got := activeFilters(c.o, "", ""); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	got := activeFilters(search.Options{Harness: "codex", Project: "api", Role: "command", Since: 24 * time.Hour}, "7d", "")
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
	// parseDur refuses a negative now (#1610), so this can no longer arrive
	// from the command line. It stays here because filterRecentSources applies
	// no time filter for one, and naming it would report a filter that never
	// ran and suppress the empty-store advice.
	if got := activeFilters(search.Options{Since: -time.Hour}, "-1h", ""); got != "" {
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

func TestLoadFileSourcesReadsEveryRegisteredStoreExceptOpencode(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	line := `{"ts":"` + now + `","project":"acme","text":"fallback store check"}` + "\n"
	if err := os.WriteFile(notes, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFileSources()
	if len(got) != 1 || got[0].Project != "acme" || len(got[0].Messages) != 1 || got[0].Messages[0].Text != "fallback store check" {
		t.Fatalf("loadFileSources() = %#v, want 1 acme note session", got)
	}
	t.Setenv("DEJA_STORES", "claude")
	if got := loadFileSources(); len(got) != 0 {
		t.Fatalf("loadFileSources() with DEJA_STORES=claude = %#v, want empty", got)
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
	res := typedOrFatal[map[string]any](t, initResp["result"])
	if res["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("bad init: %#v", initResp)
	}
	// The list is one tool with the capabilities as modes, and the modes are
	// what a reader of this test wants named.
	if !strings.Contains(lines[1], "\"name\":\"deja\"") || !strings.Contains(lines[1], "blame") ||
		!strings.Contains(lines[1], "context") || !strings.Contains(lines[2], "frobnicator bug") {
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
	if !strings.Contains(string(b), `"mcpServers"`) || !strings.Contains(string(b), `"command":"`+wantCommand+`"`) || !strings.Contains(string(b), "/bin/deja") {
		t.Fatalf("bad claude config: %s", b)
	}
	if _, err = os.Stat(path + ".bak"); err != nil {
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
	// A non-trivial injection must come with a visible receipt. No colon
	// after the name: hosts add their own introduction, and Claude Code's
	// turned it into "says: deja: recalled".
	if !strings.Contains(resp.SystemMessage, "deja recalled") || !strings.Contains(resp.SystemMessage, "prior session") {
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
	if _, err = w.WriteString(`{"session_id":"s","transcript_path":"/missing.jsonl","trigger":"auto"}`); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
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
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
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
	// The entry names the launcher, and the launcher the new binary (#5269).
	if strings.Count(string(b), "[mcp_servers.deja]") != 1 || !strings.Contains(string(b), hookExeInConfigs("/new/deja")) {
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
	if out, err := captureRun(t, "install", "--all"); err != nil || !strings.Contains(out, "no agent config found") {
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
	// gemini, cursor and grok now get their own injection point rather than MCP
	// alone — grok reads hooks from ~/.grok/hooks in the Claude Code shape.
	if out, err := captureRun(t, "install", "--auto"); err != nil ||
		!strings.Contains(out, "claude-auto:") || !strings.Contains(out, "cursor-auto:") ||
		!strings.Contains(out, "gemini-auto:") || !strings.Contains(out, "antigravity-auto:") ||
		!strings.Contains(out, "grok-auto:") {
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
		// The separator sits on our own line, so removing our entry gives the
		// reader's line back exactly as they wrote it (#2617).
		{"existing mcp no comma", "{\n  \"mcp\": {\n    \"other\": {\"type\":\"local\"}\n  }\n}\n", `"mcp"]},`},
		{"top trailing", "{\n  \"theme\": \"dark\",\n}\n", `"mcp"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := updateOpencodeJSONC([]byte(tc.old), "/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), tc.want) || !strings.Contains(string(got), `"deja"`) {
				t.Fatalf("jsonc got:\n%s\nwant contains %q", got, tc.want)
			}
			un, _, err := updateOpencodeJSONC(got, "/bin/deja", true)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(un), `"deja"`) {
				t.Fatalf("uninstall left deja: %s", un)
			}
		})
	}
	// An mcp block whose opening brace is on the next line cannot be bounded by
	// counting braces on the key's own line. Editing anyway would leave two
	// "mcp" keys and drop the user's other servers, so it errors instead.
	t.Run("mcp brace on next line errors, not corrupts", func(t *testing.T) {
		braceNext := "{\n  \"mcp\":\n  {\n    \"other\": {\"type\":\"local\"}\n  }\n}\n"
		if _, _, err := updateOpencodeJSONC([]byte(braceNext), "/bin/deja", false); err == nil {
			t.Fatal("expected an error rather than a duplicate mcp block")
		}
	})
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
	// usageText rather than printUsage through a pipe. The text is what is under
	// test, and swapping os.Stdout for a pipe hung on windows until the suite's
	// twelve-minute alarm fired, taking every later test in the package with it
	// (#1119).
	got := usageText()

	// Every flag parseSearch accepts, read from the list the parser itself uses,
	// so a flag added without a line in the help is caught here rather than by
	// the person who cannot find it.
	for _, flag := range searchFlags {
		if flag == "--rebuild" {
			// Documented with the commands that take it, not in the search block.
			continue
		}
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
	case "--session":
		return []string{"--session", "01a00feb", "q"}
	case "--sort":
		return []string{"--sort", "date", "q"}
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
	// A real harness with no sessions here (the fixture is claude-only): the
	// filter emptied the result, and the message must name it. An unknown name
	// is refused earlier now, so this path needs a valid-but-empty one.
	out, err := captureRunStderr(t, "last", "3", "--harness", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no sessions indexed yet") {
		t.Fatalf("blamed the index for a filter: %q", out)
	}
	if !strings.Contains(out, `harness "codex"`) {
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
	printNoMatches(&b, dir, "nothing", false)
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
// they deleted something that is still there under another id — and a script
// reads the code, not the wording, so `forget --session $ID && echo removed`
// printed "removed" for a session that is still there (#3601).
func TestForgetSaysWhenNothingMatched(t *testing.T) {
	seedBriefIndex(t)
	out, err := captureRun(t, "forget", "--session", "no-such-session")
	if err == nil {
		t.Fatalf("a session that is not there was reported as a success: %q", out)
	}
	if strings.Contains(out, "sessions dropped") {
		t.Fatalf("reported a removal that did not happen: %q", out)
	}
	msg := err.Error()
	if !strings.Contains(msg, "nothing matched") || !strings.Contains(msg, `session "no-such-session"`) {
		t.Fatalf("does not name the selector that came back empty: %q", msg)
	}
}

// A window is not a name: `--before 30d` on a store younger than that covers
// nothing, which is the right outcome rather than a mistake. A cleanup loop
// under `set -e` would otherwise abort on a young machine (#3601).
func TestForgetIsQuietWhenAWindowCoversNothing(t *testing.T) {
	seedBriefIndex(t)
	out, err := captureRun(t, "forget", "--before", "3650d")
	if err != nil {
		t.Fatalf("a window that covered nothing was reported as a failure: %v: %q", err, out)
	}
	if !strings.Contains(out, "nothing matched") {
		t.Fatalf("does not say the window covered nothing: %q", out)
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

// "Forgotten", "excluded by my own pattern" and "never happened" were one
// answer, and "try fewer words" is wrong counsel for the first two (#686).
func TestHiddenByOwnSettings(t *testing.T) {
	withTempStores(t)
	// Nothing configured: nothing to say. A line on every empty result is
	// noise that teaches people to skip the last line.
	if got := hiddenByOwnSettings(); got != "" {
		t.Errorf("clean machine said %q", got)
	}

	cfg := filepath.Join(os.Getenv("HOME"), ".config", "deja")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "exclude"), []byte("secret\nclientwork\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := hiddenByOwnSettings()
	if !strings.Contains(got, "2 project patterns excluded") || !strings.Contains(got, "exclude") {
		t.Errorf("exclude only: %q", got)
	}
	if strings.Contains(got, "forgotten") {
		t.Errorf("claimed forgotten sessions with no tombstones: %q", got)
	}

	// A real forget, so the tombstone is written the way the command writes it.
	seedTouchedIndex(t, 2, "/w/t/shared.go")
	if err := os.WriteFile(filepath.Join(cfg, "exclude"), []byte("secret\nclientwork\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "t00"); err != nil {
		t.Fatal(err)
	}
	got = hiddenByOwnSettings()
	if !strings.Contains(got, "forgotten") || !strings.Contains(got, "deja forget --list") {
		t.Errorf("after forget: %q", got)
	}
}

const siteRoot = "https://vshulcz.github.io/deja-vu/"

// Every manifest deja ships carries a field a directory renders as the link out
// of its listing: `homepage` in the npm, scoop and plugin manifests, `PackageUrl`
// in the winget locale, `websiteUrl` in the MCP registry entry. They pointed at
// the repository, which answers a different question than the one someone
// clicking from a plugin directory is asking.
//
// The failure this guards against is a page that moved or was never there: the
// field is a string nothing resolves, so a typo ships a 404 into someone else's
// catalogue and nothing here notices.
func TestManifestProductPagesResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	field := regexp.MustCompile(`(?m)^\s*(?:"homepage":\s*"|PackageUrl:\s*|homepage:\s*|"websiteUrl":\s*")([^"\n]+)`)

	skip := map[string]bool{"docs": true, ".github": true, "node_modules": true, "fixtures": true, "testdata": true, "dist": true, ".git": true}
	var checked int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(d.Name()) {
		case ".json", ".yaml", ".yml":
		default:
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // walks the repo's own checked-in tree
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range field.FindAllStringSubmatch(string(b), -1) {
			url := strings.TrimSpace(strings.TrimSuffix(m[1], ","))
			if !strings.HasPrefix(url, "http") {
				continue // a path or a template, not a link out
			}
			checked++
			if !strings.HasPrefix(url, siteRoot) {
				t.Errorf("%s points a directory at %s; the field is the product page, which is %s", rel, url, siteRoot)
				continue
			}
			page := strings.TrimPrefix(url, siteRoot)
			if page == "" || strings.HasSuffix(page, "/") {
				page += "index.html"
			}
			if _, err := os.Stat(filepath.Join(root, "docs", filepath.FromSlash(page))); err != nil {
				t.Errorf("%s points at %s, and docs/%s is not there", rel, url, page)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 15 {
		t.Fatalf("only %d product-page fields found; the pattern or the walk is wrong", checked)
	}

	// The registry entry is the one where the field is optional, and it was left
	// out: every published version of io.github.vshulcz/deja-vu carried
	// websiteUrl null, so the mirrors had nothing but the repository to link.
	b, err := os.ReadFile(filepath.Join(root, "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"websiteUrl"`) {
		t.Error("server.json has no websiteUrl, so the registry and its mirrors link the repository and nothing else")
	}
}

// The stats page, the viewer and the card each carried their own pasted copy of
// the mark. These pin the generated markup that replaced them, so a later change
// to the sprite that reaches the assets but not these pages fails here.
//
// The rectangles are fewer than the hand-drawn copies held: those were one per
// row, and a stretch now merges downwards through the rows that repeat it. Same
// drawing, fewer edges — and an edge is where a renderer can leave a hairline of
// background once the mark is scaled.
const (
	handDrawnStill = `<path fill="#8787af" d="M4 0h1v1h-1ZM17 0h1v1h-1ZM3 1h3v1h-3ZM16 1h3v1h-3ZM3 2h4v1h-4ZM15 2h4v1h-4ZM3 3h5v1h-5ZM14 3h5v1h-5ZM3 4h16v1h-16ZM2 5h18v2h-18ZM2 7h3v3h-3ZM7 7h8v3h-8ZM17 7h3v3h-3ZM2 10h18v1h-18ZM2 11h8v1h-8ZM12 11h8v1h-8ZM2 12h18v1h-18ZM3 13h16v1h-16ZM4 14h14v1h-14ZM19 14h2v5h-2ZM5 15h12v4h-12ZM4 19h16v2h-16ZM5 21h4v1h-4ZM13 21h4v1h-4Z"/><path fill="#1c1c1c" d="M5 7h2v3h-2ZM15 7h2v3h-2Z"/><path fill="#ff8700" d="M10 11h2v1h-2Z"/>`

	// The still layer of the animated version: no eyes cut out of it, so a blink
	// does not punch two holes in the head, and no tail, since the tail hangs
	// clear of the body and each wag position draws its own.
	handDrawnAliveBody = `<path fill="#8787af" d="M4 0h1v1h-1ZM17 0h1v1h-1ZM3 1h3v1h-3ZM16 1h3v1h-3ZM3 2h4v1h-4ZM15 2h4v1h-4ZM3 3h5v1h-5ZM14 3h5v1h-5ZM3 4h16v1h-16ZM2 5h18v6h-18ZM2 11h8v1h-8ZM12 11h8v1h-8ZM2 12h18v1h-18ZM3 13h16v1h-16ZM4 14h14v1h-14ZM5 15h12v4h-12ZM4 19h14v2h-14ZM5 21h4v1h-4ZM13 21h4v1h-4Z"/><path fill="#ff8700" d="M10 11h2v1h-2Z"/>`
)

func TestStillMarkMatchesTheCopyItReplaced(t *testing.T) {
	if got := markStill(0, 0, 1); got != handDrawnStill {
		t.Errorf("the still mark changed shape:\n got %s\nwant %s", got, handDrawnStill)
	}
}

func TestAliveMarkKeepsTheBodyAndCarriesEveryFrame(t *testing.T) {
	got := markAlive(0, 0, 1)
	if !strings.HasPrefix(got, handDrawnAliveBody) {
		t.Errorf("the animated body changed shape:\n got %s\nwant prefix %s", got, handDrawnAliveBody)
	}
	for _, class := range []string{`class="t0"`, `class="t1"`, `class="t2"`,
		`class="eyes-open"`, `class="eyes-shut"`} {
		if !strings.Contains(got, class) {
			t.Errorf("animated mark is missing %s, so that frame never shows", class)
		}
	}
}

// Both pages must actually carry the mark: a placeholder that never got
// substituted leaves a literal {{MARK_...}} in the page, and the template parses
// happily either way.
func TestPagesCarryTheMarkRatherThanThePlaceholder(t *testing.T) {
	for name, src := range map[string]string{
		"viewer":     viewSource,
		"stats page": statsHTMLSource,
	} {
		if !strings.Contains(src, "{{MARK_") {
			t.Errorf("%s source lost its mark placeholder", name)
		}
	}
	var page strings.Builder
	if err := viewTemplate.Execute(&page, viewPage{}); err != nil {
		t.Fatalf("viewer: %v", err)
	}
	out := page.String()
	if strings.Contains(out, "{{MARK_") {
		t.Error("viewer rendered with the placeholder still in it")
	}
	if !strings.Contains(out, `fill="#8787af"`) {
		t.Error("viewer rendered without the mark")
	}
}

// The floor line is read before handing a file to someone else, so the number
// has to be the number of masked spots in the file. Counting the markers *and*
// adding what the pass replaced counted a fresh secret twice (#2061).
func TestTheMaskedFloorCountsWhatIsInTheFile(t *testing.T) {
	fresh := "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcd"
	alreadyMasked, _ := redact.Text("sk-ZYXWVUTSRQPONMLKJIHGFEDCBA9876543210zyxw")
	if !strings.Contains(alreadyMasked, redact.Marker) {
		t.Fatalf("the fixture's second secret was not masked: %q", alreadyMasked)
	}

	for _, c := range []struct {
		name, title, text string
		want              int
	}{
		{"one the export catches", "pool sizing", "the key is " + fresh, 1},
		{"one already masked at ingest", "pool sizing", "the key was " + alreadyMasked, 1},
		{"one of each", "pool sizing", "now " + fresh + " and before " + alreadyMasked, 2},
		{"nothing to mask", "pool sizing", "the pool size is the fix", 0},
	} {
		path := filepath.Join(t.TempDir(), "notes.md")
		masked, err := exportPromoted(path, c.title, c.text, "claude:s9", "accepted", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// The premise and the answer are the same thing: what the line claims
		// has to be what the file holds.
		inFile := strings.Count(string(b), redact.Marker)
		if inFile != c.want {
			t.Fatalf("%s: the file holds %d masked spots, so the case is wrong: %s", c.name, inFile, b)
		}
		if masked != c.want {
			t.Errorf("%s: the line says %d, the file holds %d", c.name, masked, inFile)
		}
	}
}

// The other surface that prints the floor, counted the same way.
func TestShareCountsWhatIsInTheShare(t *testing.T) {
	fresh := "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcd"
	var out strings.Builder
	stderr := captureStderr(t, func() { printSanitized(&out, "the key is "+fresh+"\nand nothing else") })
	inShare := strings.Count(out.String(), redact.Marker)
	if inShare != 1 {
		t.Fatalf("the share holds %d masked spots, so this measures nothing: %q", inShare, out.String())
	}
	if !strings.Contains(stderr, "deja: 1 secret masked") {
		t.Errorf("the floor line disagrees with the %d masked spots in the share: %q", inShare, strings.TrimSpace(stderr))
	}
}

// The harnesses page is generated by scripts/genmatrix, and nothing checked
// that it had been re-run. It sat a store path behind the registry: newer VS
// Code Copilot Chat builds write
// `<vscode-User>/workspaceStorage/*/GitHub.copilot-chat/transcripts/*.jsonl`,
// the parser learned it, the registry recorded it, and the page — the one the
// site answers "where does my agent keep its history" with — still named the
// older directory only (#3781).
//
// TestCapabilityRegistryMatchesCode checks that a row exists per harness,
// which is why this hid: every row was there and the paths inside them are
// what drifted. Measured before the fix, exactly one of 121 paths was missing,
// so this is a precise check rather than a noisy one.
func TestTheHarnessMatrixNamesEveryStorePath(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID         string   `json:"id"`
			StorePaths []string `json:"store_paths"`
		} `json:"harnesses"`
	}
	if err = json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(root, "docs", "guide", "harnesses.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)

	checked := 0
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		for _, p := range h.StorePaths {
			checked++
			if !strings.Contains(html, p) {
				t.Errorf("the harnesses page does not name %s's %s — run `go run ./scripts/genmatrix`", h.ID, p)
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d store paths read out of the registry; this test is looking at the wrong field", checked)
	}
}

// The MCP surface stopped being six tools in #1298 and became one tool with a
// mode. Seven documents went on describing six tools — the architecture page
// listed them under a heading that said `tools/list`, the Zed extension's table
// promised six entries in the agent panel where a reader would see one, and the
// Gemini extension's own guidance named `recall_context` as a tool to call.
//
// So the rule is: a document that enumerates the surface says it is one tool,
// and names the modes the tool actually declares. The modes come from
// dejaTool() rather than a list here, which is what makes this a check rather
// than a second copy.
func TestDocsDescribingTheMCPSurfaceNameTheModesItDeclares(t *testing.T) {
	root := filepath.Join("..", "..")

	schema, ok := dejaTool()["inputSchema"].(map[string]any)
	if !ok {
		t.Fatal("the tool has no inputSchema; this test cannot read its modes")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("the schema has no properties")
	}
	mode, ok := props["mode"].(map[string]any)
	if !ok {
		t.Fatal("the schema no longer takes a mode — the surface changed shape and this rule with it")
	}
	modes, ok := mode["enum"].([]string)
	if !ok || len(modes) < 2 {
		t.Fatalf("mode enum is %v; expected the list of capabilities", mode["enum"])
	}

	// Documents that list the capabilities one by one, so a mode the tool
	// gained or lost has to reach them.
	enumerating := []string{
		"README.md",
		"SKILL.md",
		"llms-install.md",
		"docs/ARCHITECTURE.md",
		"extensions/zed/README.md",
		"extensions/kimi/README.md",
		"extensions/grok/README.md",
	}
	// Documents that describe the shape without the list. GEMINI.md and QWEN.md
	// are loaded into every session and say so: the detail stays in the tool
	// description, which is the only copy that cannot go stale.
	shapeOnly := []string{
		"GEMINI.md",
		"QWEN.md",
		"docs/guide/agents.html",
	}

	read := func(f string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			return "", false
		}
		return strings.ToLower(string(b)), true
	}
	saysOneTool := func(f, text string) {
		// Backticks vary between copies — "one tool, `deja`" and "one `deja`
		// tool" are the same sentence — so they come out for this phrase.
		plain := strings.ReplaceAll(text, "`", "")
		if !strings.Contains(plain, "one tool") && !strings.Contains(plain, "one deja tool") &&
			!strings.Contains(plain, "one mcp tool") {
			t.Errorf("%s describes the MCP server without saying it is one tool", f)
		}
	}

	for _, f := range enumerating {
		text, ok := read(f)
		if !ok {
			continue
		}
		saysOneTool(f, text)
		for _, m := range modes {
			// As code, not as prose: `how` and `fix` are ordinary words, and a
			// page dropping the `how` bullet still says "how" a dozen times.
			if !strings.Contains(text, "`"+m+"`") {
				t.Errorf("%s never names the %q mode, which the tool declares", f, m)
			}
		}
	}
	for _, f := range shapeOnly {
		text, ok := read(f)
		if !ok {
			continue
		}
		saysOneTool(f, text)
	}
}

// upgradeDayStore builds an index and then rewrites its manifest version, which
// is what a release with a format change leaves behind: the file is there, and
// this build cannot read it.
func upgradeDayStore(t *testing.T) string {
	t.Helper()
	storeWith(t, true)
	dir := index.DefaultDir()
	path := filepath.Join(dir, "manifest.gob")
	var core map[string]any
	// The manifest is a gob of a private struct, so the version is bent by
	// writing a decodable stand-in: any manifest this build cannot decode is
	// the same state to every caller under test.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("no manifest to age")
	}
	_ = core
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(struct{ Version int }{Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if !index.HasManifest(dir) {
		t.Fatal("the fixture removed the manifest instead of ageing it")
	}
	if index.IsCurrentVersion(dir) {
		t.Fatal("the fixture did not age the manifest")
	}
	if index.FormatDirection(dir) != -1 {
		t.Fatalf("the fixture produced some other broken state (%d), not an older format", index.FormatDirection(dir))
	}
	return dir
}

// The first agent call after an upgrade rebuilt the whole index inside the tool
// call and said nothing (#1309). The sentence for this state existed; its
// condition asked whether the manifest file was present, which on upgrade day
// it is.
func TestAgentToolsSayWhenTheIndexIsBeingRebuiltForThisVersion(t *testing.T) {
	dir := upgradeDayStore(t)
	line := buildingNowForAgent(dir)
	if line == "" {
		t.Fatal("an index this build cannot read was reported as ready to answer")
	}
	if !strings.Contains(line, "ask again") {
		t.Errorf("the line does not tell the agent what to do: %q", line)
	}
}

// And the tools that reach the index say it rather than rebuilding inline.
// resources/list, fix, how and blame all called the blocking index.Ensure
// (#1306); fix and how are described to the model as things to call before
// diagnosing an error or inventing a command, so they sit on the latency path
// by design.
func TestReadOnlyToolsDoNotRebuildInsideTheCall(t *testing.T) {
	dir := upgradeDayStore(t)
	for _, tc := range []struct{ name, args string }{
		{"fix", `{"error":"undefined: parseThing"}`},
		{"how", `{"what":"run the tests"}`},
		{"blame", `{"path":"cmd/deja/main.go"}`},
	} {
		out, err := callTool(t, dir, tc.name, tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !strings.Contains(out, "ask again") {
			t.Errorf("%s answered without saying the index is being rebuilt:\n%s", tc.name, out)
		}
	}
	// resources/list answers in the shape the protocol defines: an empty list,
	// not an error carrying an internal manifest path, and not a rebuild.
	res, code, msg := mcpResourcesList(dir)
	if msg != "" || code != 0 {
		t.Fatalf("resources/list returned an error rather than an empty browse: %d %s", code, msg)
	}
	body, _ := res.(map[string]any)
	list, ok := body["resources"].([]map[string]any)
	if !ok || len(list) != 0 {
		t.Errorf("resources/list served a browse from an index this build cannot read: %#v", res)
	}
	if len(body) != 1 {
		t.Errorf("resources/list result carries fields the protocol does not define: %#v", body)
	}
}

// A machine with no index at all is a first run, and building it there is the
// only way an install without hooks ever gets one. That must not change.
func TestAFirstRunStillBuildsFromTheAgentCall(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if index.HasManifest(dir) {
		t.Fatal("the fixture is not a fresh machine")
	}
	if line := buildingNowForAgent(dir); line != "" {
		t.Errorf("a fresh machine was told to ask again, and nothing would have built the index: %q", line)
	}
}

// callTool drives one MCP tool through the server the way a client does.
func callTool(t *testing.T, dir, name, args string) (string, error) {
	t.Helper()
	in := `{"jsonrpc":"2.0","id":"x","method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}` + "\n"
	var out bytes.Buffer
	if err := serveMCP(dir, strings.NewReader(in), &out); err != nil {
		return "", err
	}
	return out.String(), nil
}

// remember has to keep working: it writes the note either way, and says the
// note is not findable yet rather than rebuilding the index to make it so.
func TestRememberSavesOnUpgradeDayWithoutRebuilding(t *testing.T) {
	dir := upgradeDayStore(t)
	out, err := callTool(t, dir, "remember", `{"text":"the staging retry queue needs a longer backoff"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Saved") {
		t.Errorf("remember did not confirm the note:\n%s", out)
	}
	if !strings.Contains(out, "ask again") {
		t.Errorf("remember said nothing about the note not being findable yet:\n%s", out)
	}
}

// And a read-only index directory never repairs itself, so the agent must not
// be told to come back.
func TestNoAskAgainWhenTheIndexCannotBeRebuilt(t *testing.T) {
	dir := upgradeDayStore(t)
	// The parent, not the index directory: a rebuild writes index.db.tmp
	// beside it and renames, so that is the directory whose permissions decide
	// whether it can happen at all.
	// Windows ignores the permission bits chmod sets here, and root ignores the
	// permissions themselves, so on either the directory stays writable and the
	// state this test is about cannot be built.
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a rebuild here")
	}
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Skip("cannot make the index directory read-only here")
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	line := buildingNowForAgent(dir)
	if strings.Contains(line, "ask again") {
		t.Errorf("an agent was told to ask again about a rebuild that cannot happen: %q", line)
	}
	if !strings.Contains(line, "deja index") {
		t.Errorf("the line does not name the command that says what to change: %q", line)
	}
}

// The bundle manifest is what a desktop app reads when someone opens the .mcpb:
// it is the tool list those users see. It listed four while the server has
// served six since fix and how shipped, so two of them were invisible to
// everyone who installed that way — the same gap the README had, in a place
// nobody re-reads.
func TestBundleManifestListsEveryToolTheServerServes(t *testing.T) {
	resp, code, msg := handleMCP(t.TempDir(), rpcRequest{Method: "tools/list"})
	if code != 0 {
		t.Fatalf("tools/list: %d %s", code, msg)
	}
	payload, _ := resp.(map[string]any)
	served, ok := payload["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("tools is %T", payload["tools"])
	}

	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "mcpb", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}

	declared := map[string]bool{}
	for _, tool := range manifest.Tools {
		if tool.Description == "" {
			t.Errorf("the bundle declares %q with no description", tool.Name)
		}
		declared[tool.Name] = true
	}
	for _, tool := range served {
		name, _ := tool["name"].(string)
		if !declared[name] {
			t.Errorf("the server serves %q and the bundle manifest does not declare it", name)
		}
		delete(declared, name)
	}
	for name := range declared {
		t.Errorf("the bundle declares %q, which the server does not serve", name)
	}
}

// One store, three totals: `deja index` counted every record it wrote, `deja
// sources` every record it parsed and `deja stats` the conversation only, so
// the same index read 1340, 1386 and 1233 messages. A command, file or edit
// record is not a message; all three count the same thing now.
func TestIndexSourcesAndStatsCountTheSameMessages(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := func(m int) string { return time.Date(2026, 6, 1, 10, m, 0, 0, time.UTC).Format(time.RFC3339) }
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"` + ts(1) + `","message":{"role":"user","content":"run the pool tests"}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/work/app","timestamp":"` + ts(2) + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./internal/pool/..."}}]}}`,
		`{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"` + ts(3) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok  example.com/app/internal/pool"}]}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/work/app","timestamp":"` + ts(4) + `","message":{"role":"assistant","content":"the pool tests pass"}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	built := index.LastBuild.Messages

	parsed := sources.CountMessages(sources.LoadClaude())
	ss, err := index.SearchWithRecovery(dir, search.Options{All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	shown := stats.Build(ss, time.Now()).TotalMessages
	if built == 0 || built != parsed || built != shown {
		t.Fatalf("index %d, sources %d, stats %d — one store, three totals", built, parsed, shown)
	}
}

// roleServedStore holds a topic that exists only in records served by role: a
// command that was run and a file that was edited, never a word of prose.
func roleServedStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"carry on"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make widgetpipeline"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/app/widgetpipeline.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// Commands, file lists and edits never reach ranking, on purpose. So a search
// for a word that lives only there misses — while `how` and `blame` answer it —
// and "try fewer words" is counsel no rewording can follow (#2634).
func TestAMissPointsAtTheRecordsServedByRole(t *testing.T) {
	roleServedStore(t)
	out, err := captureRunStderr(t, "widgetpipeline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no matches") {
		t.Fatalf("the fixture no longer misses, so this pins nothing:\n%s", out)
	}
	if !strings.Contains(out, "deja how") {
		t.Fatalf("nothing says a command on this machine matches it:\n%s", out)
	}
	if !strings.Contains(out, "deja blame") {
		t.Fatalf("nothing says a file on this machine matches it:\n%s", out)
	}
}

// A miss that really is a miss keeps its short answer: a line on every failed
// search is noise that teaches people to skip the last line.
func TestAnOrdinaryMissSaysNothingExtra(t *testing.T) {
	roleServedStore(t)
	out, err := captureRunStderr(t, "zonkomatic")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deja how") || strings.Contains(out, "deja blame") {
		t.Fatalf("a word this machine never saw was pointed somewhere:\n%s", out)
	}
}

// Where a command says it found nothing, per surface.
//
// Two rules coexist in the tree and neither was written down. `search` and
// `blame` follow the one #2566 stated for `check`: stdout carries the result,
// the sentence about why there is none goes to stderr, so a run that parses
// stdout reads a miss as an empty answer rather than as prose. `how`, `files`,
// `fix` and `restore` say it on stdout, where their results would be.
//
// Both are defensible — moving `how`'s miss to stderr would hand an agent an
// empty stdout where #2634 deliberately put a pointer to `deja blame` — so this
// pins the split rather than picking a side. A new surface has to choose one
// on purpose, and a surface that changes sides has to say why (#2670).
func TestWhereEachSurfaceSaysItFoundNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		stderr bool // the miss is said on stderr, and stdout stays empty
	}{
		{name: "search", args: []string{"zonkobuffer"}, stderr: true},
		{name: "blame", args: []string{"blame", "zonko.go"}, stderr: true},
		{name: "how", args: []string{"how", "zonkoctl"}},
		{name: "files", args: []string{"files", "zonkobuffer"}},
		{name: "fix", args: []string{"fix", "panic: zonkobuffer overflow"}},
		{name: "restore", args: []string{"restore", "/w/app/zonko.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			missStore(t)
			out, err := captureRun(t, tc.args...)
			if err != nil {
				t.Fatalf("a question with no answer is not an error: %v", err)
			}
			errOut, err := captureRunStderr(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			said := strings.TrimSpace(out)
			toldOnStderr := strings.TrimSpace(errOut)
			if tc.stderr {
				if said != "" {
					t.Fatalf("stdout should stay empty for a miss here, got:\n%s", said)
				}
				if toldOnStderr == "" {
					t.Fatalf("nothing on stderr says why there is no answer")
				}
				return
			}
			if said == "" {
				t.Fatalf("this surface says its miss on stdout, and said nothing:\nstderr was:\n%s", toldOnStderr)
			}
		})
	}
}

// missStore holds work that matches none of the questions above, so every case
// is a genuine miss rather than a filtered one.
func missStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the widget pipeline keeps stalling"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply --widget 7"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// And the other half of the same fact: none of them treats a miss as an error.
// #333 and #396 settled where an exit code does belong — a refusal deja cannot
// carry out — and a question with no answer is not one.
func TestAMissIsNotAnError(t *testing.T) {
	for _, args := range [][]string{
		{"zonkobuffer"},
		{"blame", "zonko.go"},
		{"how", "zonkoctl"},
		{"files", "zonkobuffer"},
		{"fix", "panic: zonkobuffer overflow"},
		{"restore", "/w/app/zonko.go"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			missStore(t)
			if _, err := captureRun(t, args...); err != nil {
				t.Fatalf("%v exited with an error for a question it simply could not answer: %v", args, err)
			}
		})
	}
}

// Installed since: the sightings stay on file and the line went on saying the
// program was missing. On one machine all 56 of these for docker and shellcheck
// were shown after the binary was there.
func TestAProgramOnPathIsNotCalledMissing(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	dir := seedMissingProgram(t, "launchctl kickstart -k system/app.worker")
	cmd := "timeout 30 launchctl kickstart -k system/app.worker"
	if got := missingProgramLine(dir, cmd); !strings.Contains(got, "timeout is not on this machine") {
		t.Fatalf("fixture: no line while timeout is absent: %q", got)
	}
	name := "timeout"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := missingProgramLine(dir, cmd); got != "" {
		t.Errorf("timeout is on PATH and the line still says it is missing: %q", got)
	}
}

// seedMissingProgram writes two sessions that ran a wrapped command, were told
// the wrapper does not exist, and then ran the same command without it.
//
// The wrapper is `timeout` because that is what the mining recognises as a
// wrapper (`commandProgram` skips timeout, gtimeout, stdbuf and nice), and it is
// the program this machine is missing most.
func seedMissingProgram(t *testing.T, remedy string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"m1", "m2"} {
		writeClaudeFixture(t, filepath.Join(root, "-work-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:05Z","message":{"role":"user","content":"restart the worker"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"timeout 12 launchctl kickstart -k system/app.worker"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:07Z","message":{"role":"user","content":[{"type":"tool_result","content":"zsh:1: command not found: timeout"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:08Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + remedy + `"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:09Z","message":{"role":"user","content":[{"type":"tool_result","content":"done"}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The line states a fact and recommends nothing, and the note on missing_program.go
// says what that is worth: of the ten sessions told at session start that
// `timeout` was missing, nine ran it anyway. When the store holds the remedy —
// and for `timeout` on a real store both recorded pairs are the same command with
// the wrapper taken out — the line can say so.
func TestTheMissingProgramLineSaysTheCommandRanWithoutIt(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := seedMissingProgram(t, "launchctl kickstart -k system/app.worker")
	got := missingProgramLine(dir, "timeout 30 launchctl kickstart -k system/app.worker")
	if !strings.Contains(got, "is not on this machine") {
		t.Fatalf("the missing program was not named at all: %q", got)
	}
	if !strings.Contains(got, "ran without it") {
		t.Errorf("the recorded remedy did not reach the line: %q", got)
	}
}

// A remedy that is a different command is a claim one recorded run does not
// support, and a diagnostic is not a remedy at all: the line stays a fact.
func TestADifferentCommandIsNotOfferedAsTheRemedy(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := seedMissingProgram(t, "launchctl print system/app.worker | grep state")
	got := missingProgramLine(dir, "timeout 30 launchctl kickstart -k system/app.worker")
	if !strings.Contains(got, "is not on this machine") {
		t.Fatalf("the missing program was not named at all: %q", got)
	}
	if strings.Contains(got, "ran without it") {
		t.Errorf("a different command was offered as the same one: %q", got)
	}
}

// The one thing deja can say for certain before a command runs. It has been in
// the session-start block for a long time and it is measurably not heard: of
// the ten sessions on this machine that were told a command was missing, nine
// ran it anyway and got the same refusal.
func TestTheHookNamesAProgramThisMachineDoesNotHave(t *testing.T) {
	dir := storeThatKeepsMissing(t, "zonkotool", 3)
	line := commandHookLine(dir, "/work/app", "zonkotool --check ./src")
	if line == "" {
		t.Fatal("the hook said nothing about a command this machine has never had")
	}
	if !strings.Contains(line, "zonkotool") || !strings.Contains(line, "3 sessions") {
		t.Errorf("the line does not name the program and how often: %q", line)
	}
}

// One sighting is a machine that may have changed since. deja does not check
// whether the program is there now, so it must not turn a single refusal into
// an instruction.
func TestOneRefusalIsNotEnough(t *testing.T) {
	dir := storeThatKeepsMissing(t, "zonkotool", 1)
	if line := commandHookLine(dir, "/work/app", "zonkotool --check ./src"); line != "" {
		t.Errorf("one sighting was enough to warn: %q", line)
	}
}

// The wrapper is a program too, and it is the one this machine is missing most.
func TestAMissingWrapperIsFound(t *testing.T) {
	dir := storeThatKeepsMissing(t, "zonkotool", 3)
	line := commandHookLine(dir, "/work/app", "zonkotool 30 go test ./... -count=1")
	if !strings.Contains(line, "zonkotool") {
		t.Errorf("a missing wrapper in front of a real command was not named: %q", line)
	}
}

// storeThatKeepsMissing builds a store where n separate sessions were refused
// the same program.
func storeThatKeepsMissing(t *testing.T, prog string, n int) string {
	t.Helper()
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for i := range n {
		id := "s" + string(rune('a'+i))
		writeClaudeFixture(t, filepath.Join(claudeRoot, "app", id+".jsonl"), id, []string{
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + ts +
				`","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"` + prog + ` --check ./src"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + ts +
				`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"zsh:1: command not found: ` + prog + `"}]}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A search that finds nothing is the ordinary answer to a question the history
// cannot settle, and it exits 0. A command name that does not exist is not:
// `deja unforget x` did nothing, said so, and exited 0 — so a script that ran
// it and checked the code was told the work had been done.
func TestAMistypedCommandExitsNonZeroAndAMissDoesNot(t *testing.T) {
	seedOneSession(t)

	for _, tc := range []struct {
		name    string
		args    []string
		wantErr bool
		says    string
	}{
		{
			name:    "a command nobody spelled right",
			args:    []string{"unforget", "nope"},
			wantErr: true,
			says:    "`deja forget --unforget <id>`",
		},
		{
			name:    "a command one letter off",
			args:    []string{"doctro"},
			wantErr: true,
			says:    "did you mean `deja doctor`",
		},
		{
			name: "a search that found nothing",
			args: []string{"zzzqqq-nothing-here"},
			says: "no matches",
		},
		{
			name: "a search that found something",
			args: []string{"exporter"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			said, err := captureRunStderr(t, tc.args...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error = %v (stderr %q)", err, tc.wantErr, said)
			}
			if tc.wantErr && !errors.Is(err, errAlreadySaid) {
				t.Fatalf("err = %v, want the one that exits without saying anything more", err)
			}
			if tc.says != "" && !strings.Contains(said, tc.says) {
				t.Fatalf("stderr = %q, want it to say %q", said, tc.says)
			}
		})
	}
}

// The sentence is printed once, by the command rather than by main: the exit
// code is all errAlreadySaid adds.
func TestTheMistypedCommandSentenceIsNotPrintedTwice(t *testing.T) {
	seedOneSession(t)
	said, err := captureRunStderr(t, "doctro")
	if err == nil {
		t.Fatal("a mistyped command exited 0")
	}
	if n := strings.Count(said, "is not a command"); n != 1 {
		t.Fatalf("the sentence appears %d times:\n%s", n, said)
	}
	if strings.Contains(said, "already said") {
		t.Fatalf("the sentinel reached the screen:\n%s", said)
	}
}

// A typo whose word is in the history is a different case: the search answered,
// so the run succeeded and the hint is a footnote on stderr (#2197).
func TestAMistypedCommandThatMatchedSomethingStillExitsZero(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claude, "alpha", "one.jsonl"), "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"` + at +
			`","message":{"role":"user","content":"we ran deja stats and doctro on the box"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	said, err := captureRunStderr(t, "doctro")
	if err != nil {
		t.Fatalf("err = %v — the search answered, so the run worked", err)
	}
	if !strings.Contains(said, "did you mean `deja doctor`?") {
		t.Fatalf("the hint is gone:\n%s", said)
	}
}

func seedOneSession(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claude, "app", "one.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"` + at +
			`","message":{"role":"user","content":"the exporter drops every third retry"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// A note bucket's id carries the day it was minted in, so a machine that
// changes zone renames its buckets on the next build. Every id refusal then
// read as "that note is gone" while the note sat under the neighbouring day
// (#1039).
func TestAStaleBucketIdSaysTheDaysRegrouped(t *testing.T) {
	tmp := hermeticEnv(t)
	saved := time.Local
	time.Local = time.FixedZone("test+02", 2*60*60)
	t.Cleanup(func() { time.Local = saved })
	notes := filepath.Join(tmp, "notes.jsonl")
	body := `{"ts":"2026-07-16T13:40:25Z","project":"edge","text":"keep the retry budget at three"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	// East of the writer, and rebuilt: the bucket is renamed to the next day.
	time.Local = time.FixedZone("test+14", 14*60*60)
	if _, err := captureRunStderr(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	stale, moved := "deja-2026-07-16-edge", "deja-2026-07-17-edge"

	if _, err := captureRun(t, "show", stale); err == nil {
		t.Fatal("the stale id resolved")
	} else if !strings.Contains(err.Error(), moved) {
		t.Errorf("show does not name the id the note moved to: %v", err)
	}
	if _, err := captureRun(t, "promote", stale, "--state", "stale"); err == nil {
		t.Fatal("the stale id resolved for promote")
	} else if !strings.Contains(err.Error(), moved) {
		t.Errorf("promote does not name the id the note moved to: %v", err)
	}
	// The refusal carries the hint: a selector that named nothing is a failure
	// since #3601, and the id it moved to is the useful half of the answer.
	if _, err := captureRun(t, "forget", "--session", stale); err == nil {
		t.Fatal("the stale id resolved for forget")
	} else if !strings.Contains(err.Error(), moved) {
		t.Errorf("forget does not name the id the note moved to: %v", err)
	}

	// An id that never existed keeps the plain refusal — the hint is a fact
	// about this index, not a guess.
	if _, err := captureRun(t, "show", "deja-2020-01-01-nope"); err == nil {
		t.Fatal("an invented id resolved")
	} else if strings.Contains(err.Error(), "regrouped") {
		t.Errorf("an id with no neighbour was explained anyway: %v", err)
	}
}

// The hint names a session, and naming it is recalling it: under a rule that
// hides the session, the hint must go quiet too — otherwise it is the way
// around the rule. And ctx, the one command that honours the rule on an
// explicit id, was the one command that never offered the hint (#1043).
func TestTheMovedBucketHintFollowsThePolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	saved := time.Local
	time.Local = time.FixedZone("test+02", 2*60*60)
	t.Cleanup(func() { time.Local = saved })
	notes := filepath.Join(tmp, "notes.jsonl")
	body := `{"ts":"2026-07-16T13:40:25Z","project":"edge","text":"the vault rotation stays weekly"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	time.Local = time.FixedZone("test+14", 14*60*60)
	if _, err := captureRunStderr(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	stale, moved := "deja-2026-07-16-edge", "deja-2026-07-17-edge"

	// ctx gets the same way forward the human gets on show.
	if _, err := captureRun(t, "ctx", stale); err == nil {
		t.Fatal("the stale id resolved")
	} else if !strings.Contains(err.Error(), moved) {
		t.Errorf("ctx does not explain the moved id: %v", err)
	}

	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"local":false,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)
	for _, cmd := range [][]string{{"ctx", stale}, {"show", stale}} {
		_, err := captureRun(t, cmd...)
		if err == nil {
			t.Fatalf("%v resolved a hidden session", cmd)
		}
		if strings.Contains(err.Error(), moved) {
			t.Errorf("%v named a session the rule hides: %v", cmd, err)
		}
	}
}

func gitRepoWithCommit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	file := filepath.Join(root, "app.go")
	if err := os.WriteFile(file, []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "app.go")
	run("commit", "-qm", "first")
	return file
}

func TestFilesMovedSinceCountsCommitsAfterTheSession(t *testing.T) {
	file := gitRepoWithCommit(t)
	// The session ended a day before the commit, so the file moved under it.
	since := time.Now().Add(-24 * time.Hour)
	r := filesMovedSince([]string{file}, since, 5*time.Second)
	if r.Changed != 1 {
		t.Fatalf("changed=%d looked=%d, want one file changed", r.Changed, r.Looked)
	}
	if movedNote(r) != "  1 file this session touched has changed since" {
		t.Fatalf("note = %q", movedNote(r))
	}
}

func TestFilesMovedSinceStaysSilentOnFreshSessions(t *testing.T) {
	file := gitRepoWithCommit(t)
	// Nothing can have been committed after a session that ended a minute ago,
	// and asking would cost a fork on the common search.
	r := filesMovedSince([]string{file}, time.Now().Add(-time.Minute), 5*time.Second)
	if r.Changed != 0 || r.Looked != 0 {
		t.Fatalf("%+v, want no work done at all", r)
	}
}

func TestFilesMovedSinceSkipsFilesOlderThanTheSession(t *testing.T) {
	file := gitRepoWithCommit(t)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	// mtime before the session: it cannot have changed since, and the answer
	// costs a stat rather than a git invocation.
	r := filesMovedSince([]string{file}, time.Now().Add(-24*time.Hour), 5*time.Second)
	if r.Changed != 0 {
		t.Fatalf("changed=%d, want the mtime shortcut to answer", r.Changed)
	}
	if r.Looked != 1 {
		t.Fatalf("looked=%d, want the file counted as considered", r.Looked)
	}
}

func TestFilesMovedSinceIgnoresWhatItCannotResolve(t *testing.T) {
	// A path that no longer exists says nothing in either direction: it may
	// have been renamed, or the checkout may live elsewhere now.
	r := filesMovedSince([]string{filepath.Join(t.TempDir(), "gone.go")},
		time.Now().Add(-24*time.Hour), time.Second)
	if r.Changed != 0 || r.Looked != 0 {
		t.Fatalf("%+v, want silence", r)
	}
	if movedNote(r) != "" {
		t.Fatalf("note = %q, want nothing", movedNote(r))
	}
	if got := filesMovedSince(nil, time.Now().Add(-time.Hour), time.Second); got.Looked != 0 {
		t.Fatal("no paths, no work")
	}
	if got := filesMovedSince([]string{"/x"}, time.Time{}, time.Second); got.Looked != 0 {
		t.Fatal("a session with no end time cannot be compared against")
	}
}

func TestMovedNoteWording(t *testing.T) {
	if movedNote(movedReport{}) != "" {
		t.Fatal("nothing changed, nothing said")
	}
	if got := movedNote(movedReport{Changed: 4, Looked: 6}); got != "  4 files this session touched have changed since" {
		t.Fatalf("got %q", got)
	}
}

func TestGitRootOfWalksUpAndTerminates(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitRootOf(filepath.Join(deep, "x.go")); got != "" {
		t.Fatalf("no repository above it, got %q", got)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitRootOf(filepath.Join(deep, "x.go")); got != root {
		t.Fatalf("got %q, want %q", got, root)
	}
	// Terminates at the volume root rather than spinning there, which is the
	// Windows failure this shape of loop keeps producing.
	done := make(chan string, 1)
	go func() {
		done <- gitRootOf(filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "nowhere", "x.go"))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("gitRootOf did not terminate")
	}
}

// End-to-end guard for #1098: an accented word stored decomposed (NFD, "e" +
// U+0301) must be found by a query typed precomposed (NFC, U+00E9), and the
// reverse. This exercises the whole CLI path — ingest canonicalisation, the
// postings, and the search package's snippet layer — where the index-level
// test alone would not, because search.RunDetailed re-filters on the raw query.
func TestSearchFindsAccentAcrossNormalisation(t *testing.T) {
	const nfc = "caf\u00e9"       // café precomposed: U+00E9
	const nfd = "caf\u0065\u0301" // café decomposed: e + U+0301

	forms := map[string]string{"nfc": nfc, "nfd": nfd}
	for sName, sForm := range forms {
		for qName, qForm := range forms {
			t.Run(sName+"_stored_"+qName+"_query", func(t *testing.T) {
				hermeticEnv(t)
				store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				line := `{"type":"user","message":{"role":"user","content":"deployed to the ` + sForm + ` server"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"a1","cwd":"/proj"}`
				if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := captureRun(t, "index"); err != nil {
					t.Fatal(err)
				}
				out, err := captureRun(t, qForm)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, "a1") {
					t.Fatalf("stored %s, query %s: session a1 not in output:\n%s", sName, qName, out)
				}
			})
		}
	}
}

// TestNoHomeHelperProcess is one deja invocation in a process of its own, with
// no home directory and a working directory of the caller's choosing. In-process
// is not enough here: the writes this is about come from a detached warmup the
// parent does not wait for, so a glob taken when the call returns sees nothing
// whether or not the guard is there.
func TestNoHomeHelperProcess(_ *testing.T) {
	args, running := os.LookupEnv("DEJA_NO_HOME_ARGS")
	if !running {
		return
	}
	// TestMain gives every test in this package a home of its own, so the
	// child has to take it back off before it asks deja anything.
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "DEJA_INDEX_DIR"} {
		if err := os.Unsetenv(k); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	var argv []string
	if args != "" {
		argv = strings.Split(args, "\x1f")
	}
	code := 0
	if err := run(argv); err != nil {
		code = 1
	}
	os.Exit(code)
}

// runWithNoHome runs deja in an empty directory with nothing to say where its
// index goes, and answers with the exit code and whatever it left behind.
func runWithNoHome(t *testing.T, args ...string) (int, []string) {
	t.Helper()
	wd := t.TempDir()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNoHomeHelperProcess$")
	cmd.Dir = wd
	cmd.Stdin = strings.NewReader("{}")
	cmd.Env = append(os.Environ(),
		"DEJA_NO_HOME_ARGS="+strings.Join(args, "\x1f"),
		"HOME=", "USERPROFILE=", "XDG_CONFIG_HOME=", "XDG_CACHE_HOME=", "DEJA_INDEX_DIR=")
	out, runErr := cmd.CombinedOutput()
	if testing.Verbose() {
		t.Logf("%v said: %s", args, out)
	}
	code := 0
	if err := runErr; err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("%v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	// The warmup is detached, so what it writes lands after the process is
	// gone: wait for it rather than racing it. Every clean arm pays the whole
	// wait, but the arms wait side by side, so it can be long enough for a
	// warmup slowed by -race on a busy runner.
	var left []string
	for range 40 {
		found, err := filepath.Glob(filepath.Join(wd, "*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(found) > 0 {
			left = found
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for i, p := range left {
		left[i] = strings.TrimPrefix(p, wd+string(filepath.Separator))
	}
	return code, left
}

// Every path deja writes hangs off the home directory, and it answers "" when
// there is none — so `filepath.Join("", ".cache", "deja")` is `.cache/deja`,
// and deja built its index in whatever directory it was run from. #1690 fixed
// install; the rest of the commands, including the ones an agent runs
// unattended, still wrote relative (#1692).
func TestWithNoHomeNothingIsWrittenWhereDejaHappensToRun(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		// A hook must never cost a turn, so it declines quietly; a command
		// somebody typed says why; a command that writes nothing anyway just
		// runs.
		wantCode int
	}{
		{args: []string{"search", "foo"}, wantCode: 1},
		{args: []string{"retry", "budget"}, wantCode: 1},
		{args: []string{"show", "1"}, wantCode: 1},
		{args: []string{"last"}, wantCode: 1},
		{args: []string{"index"}, wantCode: 1},
		{args: []string{"aider"}, wantCode: 1},
		{args: []string{"goose"}, wantCode: 1},
		{args: []string{"mcp"}, wantCode: 1},
		{args: []string{"hook-context"}},
		{args: []string{"hook-prompt"}},
		{args: []string{"hook-tool"}},
		{args: []string{"hook-precompact"}},
		{args: []string{"statusline"}},
		// These write nothing without a home, and refusing them takes away the
		// commands a reader needs precisely when deja cannot find one.
		{args: []string{"version"}},
		// Both spellings of the same command answer the same way, and neither
		// falls through to the bare-query path, which builds an index.
		{args: []string{"--version"}},
		{args: []string{"-version"}},
		// Not a command at all: allowlisting it let it past the guard, miss
		// the dispatch map and land in the query path.
		{args: []string{"-v"}, wantCode: 1},
		// No arguments at all, which is the brief on a terminal.
		{args: nil, wantCode: 1},
		{args: []string{"help"}},
		{args: []string{"completion", "bash"}},
		{args: []string{"sources"}},
		{args: []string{"doctor"}},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			// Each arm is a process of its own in a directory of its own, and
			// a clean one spends its time waiting out the warmup: run together,
			// the arms cost one wait instead of twenty-two.
			t.Parallel()
			code, left := runWithNoHome(t, c.args...)
			if code != c.wantCode {
				t.Errorf("exit %d, want %d", code, c.wantCode)
			}
			if len(left) > 0 {
				t.Errorf("deja wrote into the working directory: %v", left)
			}
		})
	}
}

// install keeps its own refusal: it is the one that can say which account to
// wire, and the general one would send the reader after DEJA_INDEX_DIR, which
// does not help install at all.
func TestInstallKeepsItsOwnRefusal(t *testing.T) {
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "DEJA_INDEX_DIR"} {
		t.Setenv(k, "")
	}
	if stop, err := homelessRefusal("install"); stop {
		t.Errorf("install was refused by the general guard: %v", err)
	}
	stop, err := homelessRefusal("search")
	if !stop || err == nil {
		t.Fatal("a command that writes an index was allowed to run")
	}
	if !strings.Contains(err.Error(), "HOME") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
	// A command deja runs is named; a word the reader typed is not, because
	// "retry cannot find your home directory" reads as if retry were one.
	if !strings.HasPrefix(err.Error(), "search ") {
		t.Errorf("a real command is not named in its own refusal: %v", err)
	}
	_, err = homelessRefusal("budget")
	if err == nil || !strings.HasPrefix(err.Error(), "deja ") {
		t.Errorf("a query word was named as a command: %v", err)
	}
}

// doctor is where the refusal sends the reader, so it runs without a home —
// but --deep takes the index lock first, which left a lock file in whatever
// directory they were standing in.
func TestDoctorDeepDoesNotTakeALockItCannotHold(t *testing.T) {
	code, left := runWithNoHome(t, "doctor", "--deep")
	if code == 0 {
		t.Error("deep verify of an index that cannot exist reported success")
	}
	if len(left) > 0 {
		t.Errorf("doctor --deep wrote into the working directory: %v", left)
	}
}

// A headless setup with HOME unset but XDG_CACHE_HOME and XDG_CONFIG_HOME set
// to absolute paths should pass homelessRefusal and report its unbuilt index
// under XDG_CACHE_HOME rather than treating the empty cache dir as unmounted.
func TestXDGCacheHomeWithoutHomeOrExistingIndex(t *testing.T) {
	xdgCache := t.TempDir()
	xdgConfig := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "DEJA_INDEX_DIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CACHE_HOME", xdgCache)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)

	if stop, err := homelessRefusal("search"); stop || err != nil {
		t.Fatalf("homelessRefusal(search) with XDG_CACHE_HOME & XDG_CONFIG_HOME = (%v, %v), want (false, nil)", stop, err)
	}

	var out strings.Builder
	wantDir := filepath.Join(xdgCache, "deja", "index.db")
	if err := runDoctor(&out, []string{"--offline"}, offlineLookup, wantDir); err != nil {
		t.Fatalf("runDoctor failed: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, wantDir) {
		t.Errorf("doctor output did not mention %s:\n%s", wantDir, s)
	}
	if !strings.Contains(s, "status   not built") {
		t.Errorf("doctor misreported empty XDG_CACHE_HOME dir as unmounted:\n%s", s)
	}
}

// offlineLookupCalls counts the times a test took the stubbed version lookup
// TestMain installs. It is the evidence that the doctor path did not reach
// api.github.com, which the suite did eleven times per run (#2206).
var offlineLookupCalls atomic.Int64

// offlineLookup is what TestMain puts in doctorLookup's place: the answer a
// machine with no answer gives, which is the state doctor already handles.
func offlineLookup() (string, bool) {
	offlineLookupCalls.Add(1)
	return "", false
}

// Removing the stub from TestMain puts the network back under every test that
// runs doctor, and this is what notices: the counter stays where it was.
func TestDoctorDoesNotReachTheNetwork(t *testing.T) {
	hermeticEnv(t)
	before := offlineLookupCalls.Load()
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: this really ran the report, so a lookup that did not happen
	// is a lookup that went somewhere else.
	if !strings.Contains(out, "Harness stores:") {
		t.Fatalf("doctor printed something else entirely:\n%s", out)
	}
	if got := offlineLookupCalls.Load(); got == before {
		t.Errorf("doctor asked for the latest version without going through the stub, so it asked GitHub")
	}
}

// A day of notes is one session whose id *is* a date. Reading its timestamp in
// the reader's zone put a different day on the line than the id sitting beside
// it, and an id rebuilt from what the reader saw matched nothing (#883). The
// day both carry is the reader's, so the note sits in the same calendar as the
// sessions listed around it (#911).
func TestANoteBucketShowsTheDayItsIdNames(t *testing.T) {
	tmp := hermeticEnv(t)
	// time.Local is resolved once at process start, so TZ= in the environment
	// does not move it. The bug only shows outside UTC, and CI runs in UTC —
	// so the zone is set on the package's clock and restored after.
	savedLocal := time.Local
	time.Local = time.FixedZone("test+03", 3*60*60)
	t.Cleanup(func() { time.Local = savedLocal })
	notes := filepath.Join(tmp, "notes.jsonl")
	body := `{"ts":"2026-07-16T21:01:00Z","project":"edge","text":"note just after midnight in Moscow"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deja-2026-07-17-edge") {
		t.Fatalf("bucket id changed: %q", out)
	}
	// The date on the line is the date in the id, not the reader's rendering
	// of a moment inside it.
	if !regexp.MustCompile(`Jul 17\s+deja-2026-07-17-edge`).MatchString(out) {
		t.Errorf("line and id disagree:\n%s", out)
	}

	// A transcript is still shown in the reader's zone: the rule is about
	// deja's own day buckets, not about every session (#849).
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err = os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Its id even looks like a bucket id: the rule keys on the harness, not on
	// the shape of the string.
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-16T21:01:00Z","sessionId":"deja-2026-07-16-lookalike","cwd":"/proj"}`
	if err = os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`Jul 17\s+deja-2026…-lookalike`).MatchString(out) {
		t.Errorf("a transcript stopped following the reader's zone:\n%s", out)
	}
}

// Borrowing the id's day put an older date on the newest row: read from far
// enough east of the machine that minted the bucket, the column ran 06, 07, 04
// down the screen (#1038). The id is printed whole on the same line, so the
// date column can be the reader's calendar like every other row.
func TestTheDateColumnRunsOneWayDownTheScreen(t *testing.T) {
	tmp := hermeticEnv(t)
	savedLocal := time.Local
	// Minted west of the reader: the bucket id takes the writing machine's
	// day, and the reader is far enough east to be on the next one.
	time.Local = time.FixedZone("test+02", 2*60*60)
	t.Cleanup(func() { time.Local = savedLocal })
	notes := filepath.Join(tmp, "notes.jsonl")
	// A day bucket minted west of the reader, and a promoted note written a
	// moment later — the two rows that disagreed.
	// The bucket is the newest row: that is where borrowing its id's day put
	// an older date above a newer one.
	body := `{"kind":"promoted","ts":"2026-07-16T13:40:25Z","project":"edge","session":"claude:s1","state":"accepted","title":"the pooling decision","text":"decided to cap the pool"}` + "\n" +
		`{"ts":"2026-07-16T13:40:26Z","project":"edge","text":"a hand note written now"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	time.Local = time.FixedZone("test+14", 14*60*60)
	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	dates := []string{}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		parts := regexp.MustCompile(`\s{2,}`).Split(line, -1)
		if len(parts) < 3 {
			continue
		}
		day, err := time.Parse("Jan 2", parts[1])
		if err != nil {
			t.Fatalf("no day in %q", line)
		}
		dates = append(dates, day.Format("01-02"))
	}
	if len(dates) < 2 {
		t.Fatalf("expected at least two rows to compare:\n%s", out)
	}
	for i := 1; i < len(dates); i++ {
		if dates[i] > dates[i-1] {
			t.Errorf("row %d is dated after the row above it (%s then %s):\n%s", i, dates[i-1], dates[i], out)
		}
	}
}

// noteAndSourceStore is one promoted session, its note, and enough neighbours
// for the lexical scores to spread: the rerank blends a normalised BM25 with
// the cosine, so a store where the note is both the top and the bottom of the
// lexical range cannot express the ordering this is about.
func noteAndSourceStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-tmp-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileMkdir(t, filepath.Join(root, "longs.jsonl"),
		`{"type":"user","sessionId":"longs","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"the goblin pool deadlocks under load"}}`+"\n"+
			`{"type":"assistant","sessionId":"longs","timestamp":"2026-01-01T00:01:00Z","message":{"role":"assistant","content":"we raised the goblin pool size to 32 and the deadlock went away"}}`+"\n")
	writeFileMkdir(t, filepath.Join(root, "other.jsonl"),
		`{"type":"user","sessionId":"other","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"goblin pool notes from an unrelated session"}}`+"\n")
	for _, f := range []string{"f1", "f2", "f3"} {
		writeFileMkdir(t, filepath.Join(root, f+".jsonl"),
			`{"type":"user","sessionId":"`+f+`","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"filler goblin pool goblin pool goblin pool deadlock"}}`+"\n")
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(index.DefaultDir(), []string{"longs"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	// Again, so the note itself is in the index the search reads.
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
}

// embedEndpoint stands in for the embedding service and builds the sidecar from
// it. Each record is given a vector by what it says, so a test can decide what
// the machine thinks the query is near: a promoted note's text is the one that
// carries the state marker, which is how it is told apart from the transcript
// it quotes. The query goes through here too, and is the vector the records are
// measured against.
func embedEndpoint(t *testing.T, vector func(text string) string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var b strings.Builder
		b.WriteString(`{"data":[`)
		for i, in := range req.Input {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"embedding":[` + vector(in) + `]}`)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("DEJA_EMBED_URL", ts.URL)
	if err := runEmbed(index.DefaultDir(), nil); err != nil {
		t.Fatal(err)
	}
}

func hitOrder(hits []search.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.Harness+":"+h.Session.ID)
	}
	return out
}

// placeOf is where a session sits in the answer, or -1 when it is not in it.
func placeOf(hits []search.Hit, key string) int {
	for i, h := range hits {
		if h.Session.Harness+":"+h.Session.ID == key {
			return i
		}
	}
	return -1
}

const (
	sourceKey = "claude:longs"
	noteKey   = "deja:deja-note-claude-longs"
)

// `deja promote` tells the reader the note now outranks the raw transcript.
// The ranking arranges that, and then the semantic rerank — which knows nothing
// about the two being a distillation and its source — put the transcript back
// on top: the rerank is half normalised BM25 and half cosine, so the transcript
// takes its own note back whenever the cosine gap beats the lexical one.
// In real-world stores, four queries, the note below its own session in
// every one (#2083).
func TestARerankKeepsTheNoteAboveItsSource(t *testing.T) {
	noteAndSourceStore(t)
	// The transcript is what the query is nearest, its note is not, and the
	// unrelated session sits between them. That is the machine the issue
	// reports.
	embedEndpoint(t, func(text string) string {
		switch {
		case strings.Contains(text, "[accepted]"):
			return "0,1"
		case strings.Contains(text, "unrelated"):
			return "0.6,0.8"
		case strings.Contains(text, "filler"):
			return "0,1"
		default:
			return "1,0"
		}
	})

	o := search.Options{Query: "goblin pool", Limit: 50}
	result, err := index.SearchWithRecoveryDetailed(index.DefaultDir(), o, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Run(search.WithoutIgnored(result.Sessions), o)
	if err != nil {
		t.Fatal(err)
	}
	if placeOf(hits, noteKey) > placeOf(hits, sourceKey) {
		t.Fatalf("the ranking itself did not put the note first: %v", hitOrder(hits))
	}
	ranked := maybeRerank(index.DefaultDir(), hits, o, os.Stderr)

	// The rerank has to have changed something, or this passes on a machine
	// where no reordering happened at all.
	if placeOf(ranked, sourceKey) > placeOf(ranked, "claude:other") {
		t.Fatalf("the rerank did not run: the transcript is still below the unrelated session: %v", hitOrder(ranked))
	}
	note, source := placeOf(ranked, noteKey), placeOf(ranked, sourceKey)
	if note < 0 {
		t.Fatalf("the note is not in the answer at all: %v", hitOrder(ranked))
	}
	if note > source {
		t.Errorf("the rerank put the transcript above its own note: %v", hitOrder(ranked))
	}
}

// The semantic fallback is a ranking of its own — nothing matched lexically, so
// the whole answer is the sidecar's order — and the same rule applies to it.
func TestTheSemanticFallbackKeepsTheNoteAboveItsSource(t *testing.T) {
	noteAndSourceStore(t)
	// The note and its source are both over the floor the fallback holds
	// results to, so both are in the answer and the only question is which of
	// them is first; the neighbours are under it.
	embedEndpoint(t, func(text string) string {
		switch {
		case strings.Contains(text, "[accepted]"):
			return "0.6,0.8"
		case strings.Contains(text, "unrelated"), strings.Contains(text, "filler"):
			return "0,1"
		case strings.Contains(text, "deadlock"):
			return "0.95,0.3122"
		default:
			return "1,0"
		}
	})

	o := search.Options{Query: "zebrafish telemetry", Limit: 50}
	result, err := index.SearchWithRecoveryDetailed(index.DefaultDir(), o, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Run(search.WithoutIgnored(result.Sessions), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("the words were meant to match nothing, so that the fallback is what answers: %v", hitOrder(hits))
	}
	semantic, used := maybeSemantic(index.DefaultDir(), hits, o, os.Stderr)
	if !used {
		t.Fatalf("the semantic fallback did not answer: %v", hitOrder(semantic))
	}
	note, source := placeOf(semantic, noteKey), placeOf(semantic, sourceKey)
	if note < 0 || source < 0 {
		t.Fatalf("both the note and its source should be in the answer: %v", hitOrder(semantic))
	}
	if note > source {
		t.Errorf("the semantic fallback put the transcript above its own note: %v", hitOrder(semantic))
	}
}

// A promoted note is filed under its own id, so the lifecycle state — a
// property of the decision, not of the transcript — never reached it. Recall
// shows the lines that matched, and a rejection whose words are not in the
// query never appeared: the agent was handed the accepted line of a decision
// that had been taken back (#974).
func TestAPromotedNoteCarriesItsOwnState(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"decision about the pool cap"},"timestamp":"2026-07-12T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s1", "--state", "rejected", "--note", "not after all"); err != nil {
		t.Fatal(err)
	}

	hits := []search.Hit{
		{Session: model.Session{Harness: "deja", ID: "deja-note-claude-s1"}},
		{Session: model.Session{Harness: "claude", ID: "s1"}},
	}
	attachLifecycles(dir, hits)
	if hits[0].Lifecycle != "rejected" {
		t.Errorf("the note carries lifecycle %q, want rejected", hits[0].Lifecycle)
	}
	if hits[1].Lifecycle != "rejected" {
		t.Errorf("the transcript stopped carrying its state: %q", hits[1].Lifecycle)
	}
	if !strings.Contains(lifecycleLine(hits[0]), "tried and rejected") {
		t.Errorf("the note's line does not say what happened: %q", lifecycleLine(hits[0]))
	}
	// An ordinary session with no decision on it stays unmarked.
	other := []search.Hit{{Session: model.Session{Harness: "claude", ID: "unrelated"}}}
	attachLifecycles(dir, other)
	if other[0].Lifecycle != "" {
		t.Errorf("an unrelated session was marked %q", other[0].Lifecycle)
	}
}

// A note title carries no bound into the index on purpose — the state suffix is
// what every one-line surface reads it for — so each reader clips it for its own
// layout instead. Nothing held that in place, and the comment on the exemption
// gave the wrong reason for why it was safe (#2092).
func TestEverySurfaceThatPrintsANoteTitleBoundsIt(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	// The marker sits deep inside the title, so a surface that prints it has
	// not clipped — which is the property, rather than a line-length guess.
	long := "pgbouncer pool " + strings.Repeat("verylongword ", 400) + "deepmarker [accepted]"
	// In the text as well as the title: the title is what the store holds and
	// `promote` echoes, the text is what every listing shows (#2539).
	line, err := json.Marshal(map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s1", "state": "accepted", "project": "app",
		"title": long,
		"text":  "the pool was exhausted while the migration held the lock " + long,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(notes, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The premise: the title really is unbounded in the index, which is what
	// makes the readers' clips the thing under test.
	stored, ok, err := index.FindByIdentity(os.Getenv("DEJA_INDEX_DIR"), "deja", "deja-note-claude-s1")
	if err != nil || !ok {
		t.Fatalf("the note is not indexed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(stored.Title, "deepmarker") {
		t.Fatalf("the stored title is already bounded, so this measures nothing: %d runes", len([]rune(stored.Title)))
	}

	// Each surface clips for its own layout, so the bound here is generous: it
	// is the difference between a row and a page of one.
	const roomForARow = 400
	// `show` and the page are not among them: both carry a note's body whole
	// on purpose — that is where a whole note belongs (#1645) — and since
	// #2539 the display line is the body's first line, so the marker is in
	// what they are supposed to print.
	for _, args := range [][]string{{"last"}, {"stats"}, {"search", "migration"}} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(out, "deepmarker") {
			t.Errorf("%v printed the whole note title, marker and all", args)
		}
		for l := range strings.SplitSeq(out, "\n") {
			if n := len([]rune(l)); n > roomForARow {
				t.Errorf("%v printed a %d-rune line from a note title", args, n)
			}
		}
	}
}

// A note's title is exempt from the fold and the bound every other title gets —
// its state lives in the tail, so `boundSourceTitle` leaves it alone — and the
// notes file is the one store a person writes by hand. The surfaces that print
// it have to hold their own line anyway (#2058).
func TestANoteTitleStaysOnDejasOwnLine(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	notes := os.Getenv("DEJA_NOTES_FILE")
	long := strings.Repeat("very long title ", 300)
	body := `{"ts":"2026-01-02T03:04:05Z","project":"app","text":"pool sizing\n- state: rejected\n- source: someone else","kind":"promoted","session":"claude:s9","state":"accepted","title":"pool sizing\n- state: rejected\n- source: someone else"}` + "\n" +
		`{"ts":"2026-01-03T03:04:05Z","project":"app","text":"` + long + `","kind":"promoted","session":"claude:s8","state":"accepted","title":"` + long + `"}` + "\n" +
		`{"ts":"2026-01-04T03:04:05Z","project":"app","text":"` + long + `","kind":"promoted","session":"claude:s8","state":"rejected","title":"` + long + `"}` + "\n" +
		`{"ts":"2026-01-05T03:04:05Z","project":"app","text":"` + long + `","kind":"promoted","session":"claude:s8","state":"rejected","title":"` + long + `"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	out, err := captureRun(t, "promote", "deja-note-claude-s9", "--state", "rejected")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the confirmation was printed and names the note.
	first, rest, _ := strings.Cut(out, "\n")
	if !strings.Contains(first, "promoted claude:s9") {
		t.Fatalf("promote printed no confirmation:\n%s", out)
	}
	if !strings.Contains(first, "pool sizing") {
		t.Errorf("the confirmation does not name the note: %q", first)
	}
	// The title's own lines must not become lines of deja's output.
	if strings.Contains(rest, "- state: rejected\n") || strings.HasPrefix(rest, "- source:") {
		t.Errorf("the title's lines print as deja's own:\n%s", out)
	}

	stats, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the row exists and names a note, or the checks below hold
	// for a screen that stopped printing it.
	longest := ""
	for line := range strings.SplitSeq(stats, "\n") {
		if strings.Contains(line, "Longest session") {
			longest = line
		}
	}
	if longest == "" || !strings.Contains(longest, "very long title") {
		t.Fatalf("the longest session is not the note this measures:\n%s", stats)
	}
	// Bounded, and the state it ends in survives the bound: that suffix is what
	// every one-line surface reads a note title for.
	if len([]rune(longest)) > 400 {
		t.Errorf("a stats row is %d columns wide: %q", len([]rune(longest)), longest[:120])
	}
	if !strings.HasSuffix(strings.TrimSpace(longest), "[rejected]") {
		t.Errorf("the clip ate the state the title ends in: %q", longest)
	}
	// Folded into deja's row is fine; a line of its own is not — that is the
	// shape a reader takes for deja's own structure.
	for line := range strings.SplitSeq(stats, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- source:") || strings.HasPrefix(strings.TrimSpace(line), "- state:") {
			t.Errorf("a title's own line became a row of the stats screen: %q", line)
		}
	}

	// The other confirmation: the one printed when the export could not be
	// written, which is the moment a reader most needs deja's line to read as
	// deja's.
	unwritable := filepath.Join(tmp, "nowhere", "decisions.md")
	out, err = captureRun(t, "promote", "deja-note-claude-s9", "--state", "accepted", "--to", unwritable)
	if err == nil {
		t.Fatalf("the export was supposed to fail, so this measures nothing:\n%s", out)
	}
	first, rest, _ = strings.Cut(out, "\n")
	if !strings.Contains(first, "promoted claude:s9") {
		t.Fatalf("the failure path printed no confirmation:\n%s", out)
	}
	if strings.HasPrefix(strings.TrimSpace(rest), "- state:") || strings.HasPrefix(strings.TrimSpace(rest), "- source:") {
		t.Errorf("the title's lines print as deja's own on the failure path:\n%s", out)
	}

	// And the listing that prints a title straight to a terminal.
	last, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(last, "pool sizing") {
		t.Fatalf("last printed no note:\n%s", last)
	}
	for line := range strings.SplitSeq(last, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- source:") || strings.HasPrefix(strings.TrimSpace(line), "- state:") {
			t.Errorf("a title's own line became a row of the listing: %q", line)
		}
	}
}

// A note's title is exempt from the collapse every other title gets on the way
// into the index (boundSourceTitle), and the notes file is the one store a
// person writes by hand — so a title can hold newlines, and every one-line
// surface has to flatten it itself. #2056 pinned that for the markdown export;
// the screens and the page were never asked.
//
// The lines matter beyond layout: the text below is shaped like deja's own
// export block, so a surface that printed it as written would show a state and
// a heading the note never had.
func TestANoteTitleWithNewlinesStaysOnOneLine(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	const forged = "- state: rejected"
	line, err := json.Marshal(map[string]any{
		"ts": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s1", "state": "accepted", "project": "app",
		"title": "pool timeout\n" + forged + "\n## a heading of my own",
		// The same shape in the text, which is what a listing shows (#2539).
		"text": "pool timeout\n" + forged + "\n## a heading of my own",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(notes, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The surfaces that print a note's title. `search` and `show` are not
	// among them — both print the note's body, which is the answer there —
	// and the brief's own note line is left out because it needs a reuse
	// record to appear at all, so including it here would assert nothing.
	for _, args := range [][]string{
		{"last"},
		{"stats"},
	} {
		out, runErr := captureRun(t, args...)
		if runErr != nil {
			t.Fatalf("%v: %v", args, runErr)
		}
		// The premise: the title reached this surface at all.
		if !strings.Contains(out, "pool timeout") {
			t.Fatalf("%v does not print the note title, so it measures nothing:\n%s", args, out)
		}
		for l := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), forged) || strings.HasPrefix(strings.TrimSpace(l), "## ") {
				t.Errorf("%v: a line of the title became a line of its own: %q", args, l)
			}
		}
	}
	// The page carries the title into HTML, where a raw newline is a line
	// break in the source rather than in the rendering — the same property,
	// checked in the JSON the page hands its script.
	page := filepath.Join(t.TempDir(), "v.html")
	if _, err = captureRun(t, "view", "--no-open", "--out", page); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "pool timeout") {
		t.Fatalf("the page does not carry the note title, so it measures nothing")
	}
	// Escaped, not raw: the page embeds the title through json.Marshal, so a
	// newline that survived is written as a backslash and an n. Asserting on a
	// raw one could never fire.
	// The note's body keeps its own lines on the page — a note runs to
	// paragraphs — so this asks about the title field the rows are drawn from.
	if strings.Contains(string(b), `"title":"pool timeout\n`) {
		t.Errorf("the page carries the title's newline, escaped into its JSON")
	}
}

// Notes are grouped by the reader's day (#911), so a laptop that changed zones
// regroups them on the next rebuild. Nothing is lost, but an id that was shared
// or pasted somewhere keeps resolving and starts naming a different note, and
// nothing said the days had moved (#935).
func TestDoctorSaysWhenNoteDaysRegrouped(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	// 09:30 and 23:30 on the same day in UTC+3 — one bucket there, two once
	// the machine is west of UTC.
	body := `{"ts":"2026-08-02T06:30:00Z","project":"t","text":"morning: the pool cap is 20"}` + "\n" +
		`{"ts":"2026-08-02T20:30:00Z","project":"t","text":"late: the ticker window stays at 30s"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)

	saved := time.Local
	t.Cleanup(func() { time.Local = saved })

	time.Local = time.FixedZone("east", 3*60*60)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	doctorHarnesses(&out, dir)
	if strings.Contains(out.String(), "regrouped") {
		t.Errorf("an index read in the zone it was built in complained:\n%s", out.String())
	}

	// The same index, now read seven hours west: the morning note belongs to
	// the previous day here.
	time.Local = time.FixedZone("west", -7*60*60)
	out.Reset()
	doctorHarnesses(&out, dir)
	if !strings.Contains(out.String(), "regrouped") {
		t.Errorf("nothing said the days moved:\n%s", out.String())
	}

	// A note written since the build is growth, not regrouping: it must not
	// raise the warning.
	time.Local = time.FixedZone("east", 3*60*60)
	if err := os.WriteFile(notes, []byte(body+`{"ts":"2026-08-03T06:30:00Z","project":"t","text":"today: rotate the certificate"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHarnesses(&out, dir)
	if strings.Contains(out.String(), "regrouped") {
		t.Errorf("a note added since the build was called a regrouping:\n%s", out.String())
	}
}

// The notes file is the one store a person writes by hand, holding the one
// thing deja cannot re-derive, and a line it cannot read is dropped. #2005 made
// those refusals counted rather than silent; the counting is pinned for claude
// (internal/index/health_carry_test.go) and was never pinned for the store the
// hand-writing actually happens in — nor was the sentence a person sees.
func TestAHandWrittenNoteLineDejaCannotReadIsReported(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// One line in the shape the reader wants, and the shapes a person
	// plausibly writes instead: another key for the stamp and a date with no
	// time — both refused on the stamp — another key for the body, refused on
	// the empty text, and something that is not JSON, which the scanner under
	// the reader refuses before it ever gets there.
	lines := []string{
		`{"ts":"` + now + `","text":"the migration held the lock"}`,
		`{"id":"deja-note-1","time":"` + now + `","text":"the pool size is the fix"}`,
		`{"ts":"2026-01-03","text":"a date without a time"}`,
		`{"ts":"` + now + `","note":"wrong key for the body"}`,
		`not json at all`,
	}
	if err := os.WriteFile(notes, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	said, err := captureRunStderr(t, "index")
	// The index is written with a line left out, and the status says so (#5396).
	if !isExitStatus(err, exitPartial) {
		t.Fatalf("got %v, want exit status %d", err, exitPartial)
	}
	// The run says it, with the count, rather than indexing one note and
	// moving on.
	if !strings.Contains(said, "4 lines skipped") {
		t.Errorf("the index run does not name the lines it could not read:\n%s", said)
	}
	// On the store's own line, so a reader can carry the number from here to
	// doctor's JSON, where the same count is filed under the harness name.
	for line := range strings.SplitSeq(said, "\n") {
		if strings.Contains(line, "4 lines skipped") && !strings.Contains(line, "notes:") {
			t.Errorf("the count is not on the notes store's line, so nothing says whose lines they are:\n%s", line)
		}
	}
	// The note deja could read is there, so the refusals are the four and not
	// the whole file.
	if out, _ := captureRun(t, "search", "migration"); !strings.Contains(out, "migration held the lock") {
		t.Fatalf("the readable note is not in the index, so this measures nothing:\n%s", out)
	}
	if out, _ := captureRun(t, "search", "pool size"); strings.Contains(out, "pool size is the fix") {
		t.Errorf("a line deja said it could not read is in the index anyway:\n%s", out)
	}

	// doctor carries it too: the sentence a person lands on, and the count
	// under the source it belongs to.
	doc, _ := captureRun(t, "doctor")
	if !strings.Contains(doc, "unusable lines skipped") {
		t.Errorf("doctor does not mention the lines:\n%s", doc)
	}
	raw, _ := captureRun(t, "doctor", "--json")
	var report struct {
		Ingest map[string]struct {
			MalformedLines int `json:"malformed_lines"`
		} `json:"ingest_health"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json does not parse: %v", err)
	}
	total := 0
	for _, h := range report.Ingest {
		total += h.MalformedLines
	}
	if total != 4 {
		t.Errorf("doctor --json reports %d malformed lines across %d sources, want 4", total, len(report.Ingest))
	}
	// Under the harness the notes file belongs to, which is "deja" — the index
	// run calls the same store "notes", and a reader crossing from one to the
	// other has to know they are one thing.
	if n := report.Ingest["deja"].MalformedLines; n != 4 {
		t.Errorf("the notes store reports %d of them; a person fixing the file needs to know they are theirs", n)
	}
}

// Three commands write the notes file — promote, `deja remember` and the MCP
// remember tool — and only promote said what to do when the write was refused.
// The other two handed back `open …: permission denied`, over MCP to an agent
// that has to decide what to tell the user (#869).
func TestEveryNoteWriterSaysWhatToDoWhenRefused(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	if err := os.WriteFile(notes, []byte(""), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)

	want := func(t *testing.T, label string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: write into a read-only notes file succeeded", label)
		}
		got := err.Error()
		if !strings.Contains(got, "cannot write "+notes) || !strings.Contains(got, "DEJA_NOTES_FILE") {
			t.Errorf("%s: %q", label, got)
		}
		if strings.HasPrefix(got, "open ") {
			t.Errorf("%s still returns the raw syscall: %q", label, got)
		}
	}

	want(t, "remember", runRemember(filepath.Join(tmp, "idx"), []string{"a decision"}))
	want(t, "mcp remember", notesWriteError(sources.AppendNote("proj", "a decision", time.Now())))
	want(t, "promoted note", notesWriteError(sources.AppendPromoted("proj", "t", "b", "claude:x", "accepted", time.Now())))

	// A writable file still writes: the helper only rewords a refusal.
	if err := os.Chmod(notes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := notesWriteError(sources.AppendNote("proj", "a decision", time.Now())); err != nil {
		t.Errorf("writable notes file refused: %v", err)
	}
}

// npm chooses the readme it renders on the package page by looking for README*
// at the package root, and with two of them it took the translation: the page
// for dsh-deja served Chinese to everyone, which is a strange thing for an
// English-speaking user to meet on the page that is supposed to sell the
// package. One README at the root, translations in a subdirectory.
func TestPublishedPackagesHaveOneReadmeAtTheRoot(t *testing.T) {
	for _, pkg := range []string{
		filepath.Join("..", "..", "extensions", "dsh"),
		filepath.Join("..", "..", "extensions", "opencode"),
	} {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		var readmes []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(strings.ToUpper(e.Name()), "README") {
				readmes = append(readmes, e.Name())
			}
		}
		if len(readmes) != 1 {
			t.Errorf("%s has %v at its root; npm picks one of them for the package page and it is not necessarily README.md", filepath.Base(pkg), readmes)
		}

		// Whatever the allowlist ships has to exist, or the tarball quietly
		// loses a file that the README links to.
		b, err := os.ReadFile(filepath.Join(pkg, "package.json"))
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		var manifest struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(b, &manifest); err != nil {
			t.Fatalf("%s package.json: %v", pkg, err)
		}
		for _, f := range manifest.Files {
			if _, err := os.Stat(filepath.Join(pkg, f)); err != nil {
				t.Errorf("%s lists %q in files but it is not there", filepath.Base(pkg), f)
			}
		}
	}
}

func TestInstallIndexHintsTTYOnly(t *testing.T) {
	cases := []struct {
		name     string
		history  bool
		tty      bool
		want     string
		unwanted string
	}{
		{"no history", false, true, "no agent history found on this machine", "next: run"},
		{"history found", true, true, "claude-code", "no agent history"},
		{"non tty", true, false, "claude-code: created", "next: run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			if err := os.MkdirAll(sourcesClaudeConfigDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.history {
				writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "session.jsonl"), "session", []string{
					`{"type":"user","sessionId":"session","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"history"}}`,
				})
			}
			oldLogoWanted := logoWanted
			logoWanted = func(*os.File) bool { return tc.tty }
			defer func() { logoWanted = oldLogoWanted }()

			out, err := captureRun(t, "install", "--all")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) || strings.Contains(out, tc.unwanted) {
				t.Fatalf("output=%q, want %q without %q", out, tc.want, tc.unwanted)
			}
		})
	}
}

func TestInstallHintSkippedWhenIndexExists(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.gob", "sessions.gob"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("present"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := installIndexHint(index.DefaultDir()); got != "" {
		t.Fatalf("hint = %q, want empty", got)
	}
}

func TestRequestWarmupUsesRecentSentinel(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	dir := filepath.Join(t.TempDir(), "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	var calls int
	oldSpawn := spawnWarmup
	spawnWarmup = func(exe, sentinel string) error {
		calls++
		if exe == "" || sentinel != filepath.Join(dir, "warmup.sentinel") {
			t.Fatalf("warmup args = %q, %q", exe, sentinel)
		}
		return nil
	}
	defer func() { spawnWarmup = oldSpawn }()
	requestWarmup(dir)
	requestWarmup(dir)
	if calls != 1 {
		t.Fatalf("warmup calls = %d, want 1", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "warmup.sentinel")); err != nil {
		t.Fatalf("sentinel missing: %v", err)
	}
}

func TestRequestWarmupRetriesStaleAndRecordsFailure(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	dir := filepath.Join(t.TempDir(), "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error {
		calls++
		return os.ErrPermission
	}
	defer func() { spawnWarmup = oldSpawn }()
	requestWarmup(dir)
	if calls != 1 {
		t.Fatalf("stale sentinel calls = %d, want 1", calls)
	}
	b, err := os.ReadFile(sentinel)
	if err != nil || string(b) == "1\n" {
		t.Fatalf("failed warmup sentinel = %q, err=%v", b, err)
	}
}

func TestHookMissingManifestRequestsWarmup(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	dir := filepath.Join(t.TempDir(), "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	called := false
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { called = true; return nil }
	defer func() { spawnWarmup = oldSpawn }()
	if digest, sessions, _, _, _, _, _ := hookDigestResult(index.DefaultDir()); digest != "" || sessions != 0 {
		t.Fatalf("missing-manifest digest = %q, sessions=%d", digest, sessions)
	}
	if !called {
		t.Fatal("missing manifest did not request warmup")
	}
}

func TestHookContextMissingManifestStaysSilent(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index"))
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	defer func() { spawnWarmup = oldSpawn }()
	if err := runHookContext(index.DefaultDir(), true); err != nil {
		t.Fatal(err)
	}
}

func TestFirstIndexGreetingIncludesParsedZeroWarning(t *testing.T) {
	hermeticEnv(t)
	// A transcript with a turn in it that the parser cannot read — which is
	// the case worth warning about. A file holding only setup records is an
	// unused session and stays quiet.
	bad := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "bad.jsonl")
	writeFileMkdir(t, bad, `{"type":"user","message":{"role":"user","content":"hi"`+"\n")
	oldLogoWanted := logoWanted
	logoWanted = func(*os.File) bool { return true }
	defer func() { logoWanted = oldLogoWanted }()
	oldBuild := index.LastBuild
	index.LastBuild = index.BuildSummary{
		Initial: true, Messages: 1, Sessions: 1, Harnesses: 1,
		PerHarness: []index.HarnessCount{{Name: "codex", Messages: 1, Sessions: 1}},
	}
	defer func() { index.LastBuild = oldBuild }()

	out := captureStdoutCall(t, func() { maybeFirstIndexGreeting(index.DefaultDir()) })
	if !strings.Contains(out, "warning: claude files found but newest parsed to zero") {
		t.Fatalf("greeting missing parsed-zero warning: %q", out)
	}
}

func captureStdoutCall(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	// Drain concurrently: windows anonymous pipes buffer only a few KB, so
	// callbacks that print more would block a sequential read-after-call.
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	return out
}

func sourcesClaudeConfigDir() string {
	return filepath.Join(homeDir(), ".claude")
}

func TestRequestWarmupBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "warm-index")
	spawned := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
	defer func() { spawnWarmup = oldSpawn }()

	// Inside a warmup child: no recursion.
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(dir, "warmup.sentinel"))
	requestWarmup(dir)
	if spawned != 0 {
		t.Fatal("warmup child must not spawn again")
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", "")

	// First call spawns and leaves a sentinel.
	requestWarmup(dir)
	if spawned != 1 {
		t.Fatalf("spawned=%d", spawned)
	}
	// Second call within the retry window is suppressed by the sentinel.
	requestWarmup(dir)
	if spawned != 1 {
		t.Fatalf("sentinel did not suppress: spawned=%d", spawned)
	}
	// A stale sentinel is replaced and spawns again.
	stale := time.Now().Add(-2 * warmupRetryAfter).UnixNano()
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"), []byte(strconv.FormatInt(stale, 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requestWarmup(dir)
	if spawned != 2 {
		t.Fatalf("stale sentinel not replaced: spawned=%d", spawned)
	}
	// A corrupt sentinel is also replaced, once it is old enough to be nobody's
	// claim in flight: no readable stamp is exactly what a claim looks like
	// between its O_EXCL create and the write that follows (#1065).
	corrupt := filepath.Join(dir, "warmup.sentinel")
	if err := os.WriteFile(corrupt, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	requestWarmup(dir)
	if spawned != 2 {
		t.Fatalf("a sentinel with no stamp yet was taken for a dead one: spawned=%d", spawned)
	}
	when := time.Now().Add(-2 * warmupDeadAfter)
	if err := os.Chtimes(corrupt, when, when); err != nil {
		t.Fatal(err)
	}
	requestWarmup(dir)
	if spawned != 3 {
		t.Fatalf("corrupt sentinel not replaced: spawned=%d", spawned)
	}
	// The index command clears the sentinel it was born with.
	sentinel := filepath.Join(dir, "warmup.sentinel")
	t.Setenv("DEJA_WARMUP_SENTINEL", sentinel)
	clearWarmupSentinel()
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("sentinel not cleared")
	}
}

// Resume and handoff opened OpenClaw in `openclaw chat`, its local mode, where
// no plugin or hook runs, with a gateway right there whose TUI has deja's
// digest and recall (#5337). With a gateway answering they name
// `openclaw tui`; with none, `openclaw chat` and a line saying what it lacks.
func TestOpenClawResumeAndHandoffUseTheGatewayTUI(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
	dir := filepath.Join(tmp, "openclaw", "agents", "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := `{"agent:main:main":{"sessionId":"7de16196"}}`
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(store), 0o644); err != nil {
		t.Fatal(err)
	}
	s := model.Session{Harness: "openclaw", ID: "7de16196", Project: "openclaw-main", Path: filepath.Join(dir, "7de16196.jsonl")}

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", ln.Addr())
	}
	port := strconv.Itoa(tcp.Port)
	saved := openclawGatewayUp
	t.Cleanup(func() { openclawGatewayUp = saved })
	openclawGatewayUp = openclawGatewayAnswers

	// gateway.port in openclaw.json is where the TUI connects.
	if err := os.WriteFile(openclawConfigPath(), []byte(`{"gateway": {"port": `+port+`}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, cmd, err := resumeCommand(s); err != nil || cmd != "openclaw tui --session agent:main:main" {
		t.Fatalf("with a gateway up, resume gave %q, %v", cmd, err)
	}
	if argv, _ := handoffCommand("openclaw", "P"); strings.Join(argv, " ") != "openclaw tui --message P" {
		t.Fatalf("with a gateway up, handoff gave %q", argv)
	}
	if _, note := openclawTUI(); note != "" {
		t.Errorf("a note with the gateway up: %s", note)
	}

	_ = ln.Close()
	if _, cmd, err := resumeCommand(s); err != nil || cmd != "openclaw chat --session agent:main:main" {
		t.Fatalf("with no gateway, resume gave %q, %v", cmd, err)
	}
	if argv, _ := handoffCommand("openclaw", "P"); strings.Join(argv, " ") != "openclaw chat --message P" {
		t.Fatalf("with no gateway, handoff gave %q", argv)
	}
	if _, note := openclawTUI(); !strings.Contains(note, "127.0.0.1:"+port) || !strings.Contains(note, "openclaw tui") {
		t.Errorf("the local-mode line does not name the gateway or the TUI: %s", note)
	}

	// The port OpenClaw is told in its environment wins over the config, and
	// a remote gateway is where the TUI goes in remote mode.
	t.Setenv("OPENCLAW_GATEWAY_PORT", "4242")
	if got := openclawGatewayAddr(); got != "127.0.0.1:4242" {
		t.Errorf("OPENCLAW_GATEWAY_PORT: %s", got)
	}
	if err := os.WriteFile(openclawConfigPath(), []byte(`{"gateway": {"mode": "remote", "remote": {"url": "wss://gw.example.test"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := openclawGatewayAddr(); got != "gw.example.test:443" {
		t.Errorf("remote mode: %s", got)
	}
	t.Setenv("OPENCLAW_GATEWAY_URL", "ws://127.0.0.1:5151")
	if got := openclawGatewayAddr(); got != "127.0.0.1:5151" {
		t.Errorf("OPENCLAW_GATEWAY_URL: %s", got)
	}
}

// hook-context marks the session that is starting as live, which is what keeps
// it out of its own MCP recall. The pi-family extensions asked for the digest
// with empty stdin, so the mark went on no session and the asking session
// ranked first in its own first turn — the gap #4246 closed for Hermes and
// #4273 for the opencode package (#4394).
func TestPiFamilyStartSendsTheSession(t *testing.T) {
	pkg, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, src, id string }{
		{"pi-auto", piExtensionTS("/bin/deja"), "sessionID()"},
		{"omp-auto", ompExtensionJS("/bin/deja"), "sessionID()"},
		{"prime-auto", primeExtensionTS("/bin/deja"), "sessionID()"},
		{"pi package", string(pkg), "session"},
	} {
		calls := strings.Split(tc.src, `run(["hook-context"]`)[1:]
		if len(calls) == 0 {
			t.Errorf("%s: no hook-context call at all", tc.name)
			continue
		}
		for _, call := range calls {
			end := strings.Index(call, ");")
			if end < 0 {
				end = len(call)
			}
			payload := call[:end]
			if !strings.Contains(payload, "session_id: "+tc.id) || !strings.Contains(payload, "cwd:") {
				t.Errorf("%s: hook-context is asked without the session and its directory: run([\"hook-context\"]%s)", tc.name, payload)
			}
		}
	}
}
