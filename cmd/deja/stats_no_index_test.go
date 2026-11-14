package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

func setupNoIndexClaudeStore(t *testing.T, withSession bool) string {
	t.Helper()
	_ = hermeticEnv(t)
	nonexistentDir := filepath.Join(t.TempDir(), "never-created", "deja")
	t.Setenv("DEJA_DIR", nonexistentDir)
	t.Setenv("DEJA_INDEX_DIR", nonexistentDir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "xdg-cache"))
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	home := os.Getenv("HOME")
	claudeCfg := filepath.Join(home, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeCfg)
	if !withSession {
		return nonexistentDir
	}
	projDir := filepath.Join(claudeCfg, "projects", "-Users-alex-demo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"fix jwt key rotation"},"timestamp":"2026-10-01T10:00:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"rotated keys in auth middleware"},"timestamp":"2026-10-01T10:01:00Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(projDir, "session-1.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return nonexistentDir
}

func captureStatsCall(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var callErr error
	out := captureStdoutCall(t, func() {
		callErr = runStats(dir, args)
	})
	return out, callErr
}

func TestStatsNoIndexTextAndCardDoNotCreateDir(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, true)

	textOut, err := captureStatsCall(t, nonexistentDir, "--no-index")
	if err != nil {
		t.Fatalf("runStats --no-index: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created, got stat err: %v", serr)
	}
	if !strings.Contains(textOut, "local agent stores") {
		t.Fatalf("expected local agent stores subtitle, got:\n%s", textOut)
	}
	if strings.Contains(textOut, "Recall") {
		t.Fatalf("expected --no-index text report to omit Recall section, got:\n%s", textOut)
	}
	if !strings.Contains(textOut, "npx @vshulcz/deja-vu install --auto") {
		t.Fatalf("expected unwired install hint in text output, got:\n%s", textOut)
	}
	if !strings.Contains(textOut, "Sessions") || !strings.Contains(textOut, "1") || !strings.Contains(textOut, "2") {
		t.Fatalf("expected session and message counts in text output, got:\n%s", textOut)
	}

	cardOut, err := captureStatsCall(t, nonexistentDir, "--no-index", "--card")
	if err != nil {
		t.Fatalf("runStats --no-index --card: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created after --card, got stat err: %v", serr)
	}
	if !strings.Contains(cardOut, "npx @vshulcz/deja-vu install --auto") {
		t.Fatalf("expected unwired install hint in card output, got:\n%s", cardOut)
	}
}

func TestStatsNoIndexJSONDoesNotWriteIndexOrHint(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, true)

	jsonOut, err := captureStatsCall(t, nonexistentDir, "--no-index", "--json")
	if err != nil {
		t.Fatalf("runStats --no-index --json: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created after --json, got stat err: %v", serr)
	}
	if strings.Contains(jsonOut, "install --auto") {
		t.Fatalf("expected --json output not to contain install hint, got:\n%s", jsonOut)
	}
	var rep stats.Report
	if jerr := json.Unmarshal([]byte(jsonOut), &rep); jerr != nil {
		t.Fatalf("invalid json output: %v\n%s", jerr, jsonOut)
	}
	if rep.TotalSessions != 1 || rep.TotalMessages != 2 {
		t.Fatalf("unexpected report counts: sessions=%d messages=%d", rep.TotalSessions, rep.TotalMessages)
	}
}

func TestStatsNoIndexOmitsInstallHintOnceWired(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, true)

	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatalf("install claude-auto --no-index: %v", err)
	}

	textOut, err := captureStatsCall(t, nonexistentDir, "--no-index")
	if err != nil {
		t.Fatalf("runStats --no-index after install: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to exist when install used --no-index, got stat err: %v", serr)
	}
	if strings.Contains(textOut, "npx @vshulcz/deja-vu install --auto") {
		t.Fatalf("expected wired setup to omit install hint in text output, got:\n%s", textOut)
	}

	cardOut, err := captureStatsCall(t, nonexistentDir, "--no-index", "--card")
	if err != nil {
		t.Fatalf("runStats --no-index --card after install: %v", err)
	}
	if strings.Contains(cardOut, "npx @vshulcz/deja-vu install --auto") {
		t.Fatalf("expected wired setup to omit install hint in card output, got:\n%s", cardOut)
	}
}

func TestStatsNoIndexRejectsIncompatibleFlags(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, false)
	for _, flag := range []string{"--impact", "--year", "--redaction"} {
		if _, err := captureStatsCall(t, nonexistentDir, "--no-index", flag); err == nil {
			t.Fatalf("expected error for --no-index %s, got nil", flag)
		}
	}
}

func TestStatsNoIndexEmptyStoresMessage(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, false)

	out, err := captureStatsCall(t, nonexistentDir, "--no-index")
	if err != nil {
		t.Fatalf("runStats --no-index on empty stores: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created on empty stores, got stat err: %v", serr)
	}
	if !strings.Contains(out, "no agent sessions found in local stores") {
		t.Fatalf("expected empty-store message, got:\n%s", out)
	}
}

func TestStatsNoIndexHarnessAndNarrowedFilter(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, true)

	claudeJSON, err := captureStatsCall(t, nonexistentDir, "--no-index", "--harness", "claude", "--json")
	if err != nil {
		t.Fatalf("runStats --no-index --harness claude --json: %v", err)
	}
	var rep stats.Report
	if jerr := json.Unmarshal([]byte(claudeJSON), &rep); jerr != nil {
		t.Fatalf("invalid json output: %v\n%s", jerr, claudeJSON)
	}
	if rep.TotalSessions != 1 || rep.TotalMessages != 2 {
		t.Fatalf("unexpected counts for --harness claude: sessions=%d messages=%d", rep.TotalSessions, rep.TotalMessages)
	}

	narrowOut, err := captureStatsCall(t, nonexistentDir, "--no-index", "--project", "nonexistent")
	if err != nil {
		t.Fatalf("runStats --no-index --project nonexistent: %v", err)
	}
	if !strings.Contains(narrowOut, "no sessions match") {
		t.Fatalf("expected narrowed filter message, got:\n%s", narrowOut)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created after filtered runs, got stat err: %v", serr)
	}
}
