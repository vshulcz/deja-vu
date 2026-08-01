package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// README: restore "never writes over the original". Comparing -o against the
// argument alone was not enough — naming the same file two ways went through,
// and any other existing file was overwritten without a word (#725).
func TestRestoreRefusesToOverwrite(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	repo := filepath.Join(tmp, "repo")
	for _, d := range []string{root, repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	edited := filepath.ToSlash(filepath.Join(repo, "pool.go"))
	b, _ := json.Marshal(map[string]any{"file_path": edited, "old_string": "size: 200", "new_string": "size: 64"})
	body := `{"type":"user","sessionId":"s3","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":"shrink the pool"}}` + "\n" +
		`{"type":"assistant","sessionId":"s3","cwd":"/w/p","timestamp":"2026-07-22T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":` + string(b) + `}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s3.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repo, "pool.go")
	if err := os.WriteFile(source, []byte("REAL SOURCE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(tmp, "exists.txt")
	if err := os.WriteFile(other, []byte("IMPORTANT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The file the span came from, named differently from the argument.
	var buf strings.Builder
	err := runRestore(dir, []string{"pool.go", "--span", "1", "-o", source}, &buf)
	if err == nil || !strings.Contains(err.Error(), "that is the file this span came from") {
		t.Fatalf("overwrote the original: %v", err)
	}
	if got, _ := os.ReadFile(source); string(got) != "REAL SOURCE\n" {
		t.Errorf("source is now %q", got)
	}

	// Any other existing file.
	buf.Reset()
	err = runRestore(dir, []string{"pool.go", "--span", "1", "-o", other}, &buf)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrote an existing file: %v", err)
	}
	if got, _ := os.ReadFile(other); string(got) != "IMPORTANT\n" {
		t.Errorf("file is now %q", got)
	}

	// --force is the way to say it on purpose.
	buf.Reset()
	if err := runRestore(dir, []string{"pool.go", "--span", "1", "-o", other, "--force"}, &buf); err != nil {
		t.Fatalf("--force: %v", err)
	}
	if got, _ := os.ReadFile(other); string(got) != "size: 200" {
		t.Errorf("--force wrote %q", got)
	}
	// --force does not unlock the original.
	buf.Reset()
	if err := runRestore(dir, []string{"pool.go", "--span", "1", "-o", source, "--force"}, &buf); err == nil {
		t.Error("--force overwrote the file the span came from")
	}

	// A new path still just works.
	buf.Reset()
	fresh := filepath.Join(tmp, "fresh.txt")
	if err := runRestore(dir, []string{"pool.go", "--span", "1", "-o", fresh}, &buf); err != nil {
		t.Fatalf("fresh path: %v", err)
	}
	if got, _ := os.ReadFile(fresh); string(got) != "size: 200" {
		t.Errorf("fresh file has %q", got)
	}
}
