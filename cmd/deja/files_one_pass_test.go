package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja files` read its matching sessions one identity at a time, and a
// per-identity read walks the whole record log because records carry no
// per-session offset. On a 5000-session store that was 250 walks of a 59 MB
// log — 1.6 s of a 1.9 s command (#1069).
func TestFilesReadsTheRecordLogOnce(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	const sessions = 12
	for i := 0; i < sessions; i++ {
		id := fmt.Sprintf("s%02d", i)
		path := filepath.Join(repo, fmt.Sprintf("mod%02d.go", i))
		if err := os.WriteFile(path, []byte("package repo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT10:00:00Z","message":{"role":"user","content":"the migration rollback again"}}`, id, repo, i+1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT10:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, id, repo, i+1, path),
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-repo", id+".jsonl"), id, lines)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	before := index.RecordLogScans()
	out, err := captureRun(t, "files", "migration")
	if err != nil {
		t.Fatal(err)
	}
	scans := index.RecordLogScans() - before
	if !strings.Contains(out, "files touched while working on") {
		t.Fatalf("the probe measured a command that answered nothing:\n%s", out)
	}
	if scans > 1 {
		t.Errorf("files walked the record log %d times for %d sessions, want 1", scans, sessions)
	}
}

// blame had the same loop, bounded at 50 sessions — a bound on how many walks
// of the whole log, not on the work (#1069).
func TestBlameReadsTheRecordLogOnce(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "shared.go")
	if err := os.WriteFile(target, []byte("package repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	const sessions = 12
	for i := 0; i < sessions; i++ {
		id := fmt.Sprintf("b%02d", i)
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT10:00:00Z","message":{"role":"user","content":"touch the shared file"}}`, id, repo, i+1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT10:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, id, repo, i+1, target),
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-repo", id+".jsonl"), id, lines)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	before := index.RecordLogScans()
	out, err := captureRun(t, "blame", target)
	if err != nil {
		t.Fatal(err)
	}
	scans := index.RecordLogScans() - before
	if strings.TrimSpace(out) == "" {
		t.Fatalf("the probe measured a command that answered nothing")
	}
	if scans > 1 {
		t.Errorf("blame walked the record log %d times for %d sessions, want 1", scans, sessions)
	}
}
