package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The words a topic is asked in are the needles the file list anchors on, and
// they were taken from argv rather than from the query: `deja files "a b c"`
// looked for the whole sentence inside one message and found nothing, while
// `deja files a b c` — the same words, different shell quoting — answered
// (#3409).
func TestFilesAnswersTheSameQuotedOrNot(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "retry.go"), []byte("package api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-w-api", "p1.jsonl"), "p1", []string{
		`{"type":"user","sessionId":"p1","cwd":"/w/api","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer drops the last attempt of the retry loop"}}`,
		`{"type":"assistant","sessionId":"p1","cwd":"/w/api","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + jsonEscaped(t, filepath.Join(repo, "retry.go")) + `","old_string":"a","new_string":"b"}}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	split, err := captureRun(t, "files", "zonkobuffer", "retry", "loop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(split, "retry.go") {
		t.Fatalf("the fixture does not answer even word by word, so this pins nothing:\n%s", split)
	}
	quoted, err := captureRun(t, "files", "zonkobuffer retry loop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(quoted, "retry.go") {
		t.Errorf("quoting the same words empties the answer:\n%s", quoted)
	}
}
