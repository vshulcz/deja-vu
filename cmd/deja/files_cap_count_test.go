package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Every count `files` prints is taken over the sessions it read, and it reads
// at most filesMaxSessions of them. On a store where 290 sessions said the
// word the answer was "250 sessions" — the read budget presented as the size
// of the match, with nothing in the line to tell the two apart.
func TestFilesSaysWhenTheReadBudgetBit(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(repo, "shared.go")
	if err := os.WriteFile(shared, []byte("package repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	const over = 40
	total := filesMaxSessions + over
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("s%04d", i)
		day := 1 + i/24
		hour := i % 24
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT%02d:00:00Z","message":{"role":"user","content":"the migration rollback again"}}`, id, repo, day, hour),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT%02d:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, id, repo, day, hour, shared),
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-repo", id+".jsonl"), id, lines)
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "files", "migration")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "files touched while working on") {
		t.Fatalf("the probe measured a command that answered nothing:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("of %d matching sessions", total)) {
		t.Errorf("%d sessions matched, the line reports only the %d that were read:\n%s", total, filesMaxSessions, out)
	}

	// The other side: under the budget the note must not appear, or every
	// answer carries a caveat about a cap that never bit.
	small := hermeticEnv(t)
	smallRepo := filepath.Join(small, "repo")
	if err := os.MkdirAll(filepath.Join(smallRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	smallFile := filepath.Join(smallRepo, "one.go")
	if err := os.WriteFile(smallFile, []byte("package repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	smallRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	lines := []string{
		fmt.Sprintf(`{"type":"user","sessionId":"only","cwd":%q,"timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the migration rollback again"}}`, smallRepo),
		fmt.Sprintf(`{"type":"assistant","sessionId":"only","cwd":%q,"timestamp":"2026-07-01T10:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, smallRepo, smallFile),
	}
	writeClaudeFixture(t, filepath.Join(smallRoot, "projects", "-repo", "only.jsonl"), "only", lines)
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	quiet, err := captureRun(t, "files", "migration")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "most recent were read") {
		t.Errorf("one matching session, and the answer still names a read budget:\n%s", quiet)
	}
}
