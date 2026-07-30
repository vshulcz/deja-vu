package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFilesFixture lays down a claude session that discusses one subject and
// opens files while doing it, inside a directory that looks like a repository.
func writeFilesFixture(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	line := func(format string, a ...any) {
		fmt.Fprintf(&b, format+"\n", a...)
	}
	line(`{"type":"user","sessionId":"claude-files","cwd":%q,"timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the frobnicator retry loop is wrong"}}`, repo)
	// Two files opened right after the subject came up, one of them twice.
	for i, p := range []string{"retry.go", "retry.go", "loop.go"} {
		line(`{"type":"assistant","sessionId":"claude-files","cwd":%q,"timestamp":"2026-01-02T03:0%d:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":%q}}]}}`,
			repo, 5+i, filepath.Join(repo, p))
	}
	// A file opened hours later, while something else was being discussed.
	line(`{"type":"user","sessionId":"claude-files","cwd":%q,"timestamp":"2026-01-02T09:00:00Z","message":{"role":"user","content":"unrelated packaging work"}}`, repo)
	line(`{"type":"assistant","sessionId":"claude-files","cwd":%q,"timestamp":"2026-01-02T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":%q}}]}}`,
		repo, filepath.Join(repo, "packaging.go"))
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestFilesCommandRanksNearbyTouches(t *testing.T) {
	writeFilesFixture(t)
	out, err := captureRun(t, "files", "frobnicator", "retry")
	if err != nil {
		t.Fatalf("files: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "retry.go") || !strings.Contains(out, "loop.go") {
		t.Fatalf("want the files opened beside the subject, got:\n%s", out)
	}
	if strings.Contains(out, "packaging.go") {
		t.Fatalf("a file touched hours later is different work, got:\n%s", out)
	}
	if strings.Index(out, "retry.go") > strings.Index(out, "loop.go") {
		t.Fatalf("the file touched twice should rank first, got:\n%s", out)
	}
}

func TestFilesCommandSaysWhenNothingIsNear(t *testing.T) {
	writeFilesFixture(t)
	out, err := captureRun(t, "files", "packaging")
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	// The subject exists and a file was opened a minute later, so this is the
	// hit path; the refusal path is a subject nobody discussed.
	if out == "" {
		t.Fatal("expected some output")
	}
	out, err = captureRun(t, "files", "quantumfluxcapacitor")
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	if !strings.Contains(out, "no sessions mention") && !strings.Contains(out, "none of them recorded") {
		t.Fatalf("want an honest refusal, got %q", out)
	}
}

func TestFilesCommandArgumentErrors(t *testing.T) {
	hermeticEnv(t)
	if err := runFiles(t.TempDir(), nil, os.Stdout); err == nil {
		t.Fatal("no topic should be a usage error")
	}
	if err := runFiles(t.TempDir(), []string{"topic", "--limit", "zero"}, os.Stdout); err == nil {
		t.Fatal("--limit wants a number")
	}
}
