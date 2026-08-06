package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Retrieval hands candidates back in map order. `files` took the first
// filesMaxSessions of them, so on a store with more matches than the cap a
// different quarter of the sessions was counted on every run and the top file
// changed between two runs of the same binary over an unchanged index (#1072).
//
// The store here has 40 sessions past the cap. Each of the oldest 40 touches a
// file nothing else touches, so a cap that keeps the newest never reports one
// of them; a cap that keeps whatever the map yielded reports many.
func TestFilesCapKeepsTheNewestSessions(t *testing.T) {
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
	const old = 40
	total := filesMaxSessions + old
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("s%04d", i)
		// i < old are the oldest; each touches a file of its own.
		touched := shared
		if i < old {
			touched = filepath.Join(repo, fmt.Sprintf("stale%02d.go", i))
			if err := os.WriteFile(touched, []byte("package repo\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		day := 1 + i/24
		hour := i % 24
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT%02d:00:00Z","message":{"role":"user","content":"the migration rollback again"}}`, id, repo, day, hour),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-07-%02dT%02d:05:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{"file_path":%q}}]}}`, id, repo, day, hour, touched),
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-repo", id+".jsonl"), id, lines)
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	first, err := captureRun(t, "files", "migration", "--limit", "60")
	if err != nil {
		t.Fatal(err)
	}
	if stale := strings.Count(first, "stale"); stale != 0 {
		t.Errorf("the cap kept %d of the %d oldest sessions — it took whatever order retrieval yielded:\n%s", stale, old, first)
	}
	// And the same answer twice: nondeterminism is what the reader actually
	// hit, and one run cannot show it.
	second, err := captureRun(t, "files", "migration", "--limit", "60")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("two runs over an unchanged index disagreed:\n--- first\n%s\n--- second\n%s", first, second)
	}
}
