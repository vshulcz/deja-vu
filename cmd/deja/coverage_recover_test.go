package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// --- doctor.go ---

func TestDoctorSQLiteAndCursorDetailBranches(t *testing.T) {
	tmp := hermeticEnv(t)

	db := filepath.Join(tmp, "opencode.db")
	if err := os.WriteFile(db, []byte("some bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := doctorSQLiteDetail(db, false); !strings.Contains(got, "sqlite3 CLI missing") {
		t.Fatalf("doctorSQLiteDetail sqlite-missing = %q", got)
	}
	if got := doctorSQLiteDetail(db, true); got == "" || strings.Contains(got, "missing") {
		t.Fatalf("doctorSQLiteDetail sqlite-present = %q", got)
	}
	if got := doctorSQLiteDetail(filepath.Join(tmp, "absent.db"), true); got != "" {
		t.Fatalf("doctorSQLiteDetail absent = %q", got)
	}

	cursorRoot := os.Getenv("DEJA_CURSOR_ROOT")
	dbPath := filepath.Join(cursorRoot, "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("cursor db bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := doctorCursorDetail(false); !strings.Contains(got, "IDE") || !strings.Contains(got, "sqlite3 CLI missing") {
		t.Fatalf("doctorCursorDetail sqlite-missing = %q", got)
	}
	if got := doctorCursorDetail(true); !strings.Contains(got, "1 store") || strings.Contains(got, "missing") {
		t.Fatalf("doctorCursorDetail sqlite-present = %q", got)
	}
}

func TestDoctorAntigravityLocationFallback(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", "")
	home := filepath.Join(tmp, "home")
	if got := doctorAntigravityLocation(); !strings.Contains(got, home) || !strings.Contains(got, "antigravity*") {
		t.Fatalf("doctorAntigravityLocation fallback = %q", got)
	}
}

func TestDoctorOpencodeConfigPathJSONCFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "home", ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(jsonc, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := doctorOpencodeConfigPath(); got != jsonc {
		t.Fatalf("doctorOpencodeConfigPath jsonc = %q, want %q", got, jsonc)
	}
	// Plain .json wins when both are present.
	plain := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(plain, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := doctorOpencodeConfigPath(); got != plain {
		t.Fatalf("doctorOpencodeConfigPath plain = %q, want %q", got, plain)
	}
}

func TestDoctorTOMLWiredMissingFile(t *testing.T) {
	if doctorTOMLWired(filepath.Join(t.TempDir(), "absent.toml")) {
		t.Fatal("missing toml file must not be wired")
	}
}

func TestDoctorIndexBuiltBranch(t *testing.T) {
	tmp := hermeticEnv(t)
	idx := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idx)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-doc", "doc123.jsonl"), "doc123", []string{
		`{"type":"user","sessionId":"doc123","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"doctor index needle"}}`,
	})
	if err := index.EnsureForSearch(idx, search.Options{Query: "needle", All: true}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	report := collectDoctorReport(nil, index.DefaultDir())
	doctorIndex(&out, report.Index, index.DefaultDir())
	got := out.String()
	if !strings.Contains(got, "status   built") || !strings.Contains(got, "size=") || !strings.Contains(got, "updated=") {
		t.Fatalf("doctorIndex built = %q", got)
	}
	if !strings.Contains(got, "freshness up to date") {
		t.Fatalf("doctorIndex freshness = %q", got)
	}
}

// --- logo.go ---

func TestLogoWantedStatError(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "closed-logo")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if logoWanted(f) {
		t.Fatal("closed file with failing Stat must not want a logo")
	}
}

func TestMaybeFirstIndexGreetingPrints(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()

	oldStdout := os.Stdout
	os.Stdout = devNull
	defer func() { os.Stdout = oldStdout }()

	oldBuild := index.LastBuild
	defer func() { index.LastBuild = oldBuild }()
	// A multi-harness build with an empty-message entry drives both the
	// per-agent breakdown loop and its zero-message skip.
	index.LastBuild = index.BuildSummary{
		Initial:   true,
		Sessions:  3,
		Messages:  5,
		Harnesses: 2,
		PerHarness: []index.HarnessCount{
			{Name: "claude", Sessions: 2, Messages: 4},
			{Name: "codex", Sessions: 1, Messages: 1},
			{Name: "empty", Sessions: 0, Messages: 0},
		},
	}

	// Just needs to run without panicking; output goes to /dev/null and
	// there is nothing to assert on, but the branch executes.
	maybeFirstIndexGreeting(index.DefaultDir())

	// A zero-message initial build digest.Short circuits before printing.
	index.LastBuild = index.BuildSummary{Initial: true, Sessions: 1, Messages: 0, Harnesses: 1}
	maybeFirstIndexGreeting(index.DefaultDir())

	// Non-initial build must digest.Short circuit before printing.
	index.LastBuild = index.BuildSummary{Initial: false, Sessions: 1, Messages: 1, Harnesses: 1}
	maybeFirstIndexGreeting(index.DefaultDir())
}

// --- resume.go ---

func TestRunResumeResumeCommandErrorBranch(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// A codex history.jsonl entry parses to Project == "history", which
	// resumeCommand rejects with an error runResume must propagate.
	codexRoot := filepath.Join(tmp, "codex")
	if err := os.MkdirAll(codexRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	histLine := `{"session_id":"histentry","text":"resume err needle","ts":1750000000}` + "\n"
	if err := os.WriteFile(filepath.Join(codexRoot, "history.jsonl"), []byte(histLine), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	var out bytes.Buffer
	err := runResume(index.DefaultDir(), []string{"histentry"}, &out)
	if err == nil || !strings.Contains(err.Error(), "nothing to resume") {
		t.Fatalf("expected resumeCommand history error, got %v", err)
	}
}

func TestClaudeProjectDirForBaseEmptyBranch(t *testing.T) {
	// A bare relative filename has filepath.Dir == "." which ClaudeProjectDirBase
	// treats as empty, exercising claudeProjectDirFor's base=="" branch.
	if got := claudeProjectDirFor(model.Session{Path: "plain.jsonl"}); got != "" {
		t.Fatalf("claudeProjectDirFor bare relative = %q", got)
	}
}
