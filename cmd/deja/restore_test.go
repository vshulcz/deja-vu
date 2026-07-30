package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEditFixture(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line(`{"type":"user","sessionId":"claude-edit","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"clean up the retry helper"}}`)
	line(`{"type":"assistant","sessionId":"claude-edit","cwd":"/w","timestamp":"2026-01-02T03:05:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/pkg/retry.go","old_string":"func retry() error {\n\treturn nil\n}"}}]}}`)
	line(`{"type":"assistant","sessionId":"claude-edit","cwd":"/w","timestamp":"2026-01-02T03:06:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"MultiEdit","input":{"file_path":"/w/pkg/retry.go","edits":[{"old_string":"const backoff = 1"},{"old_string":"const ceiling = 9"}]}}]}}`)
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func TestRestoreListsAndWritesSpans(t *testing.T) {
	writeEditFixture(t)
	out, err := captureRun(t, "restore", "retry.go")
	if err != nil {
		t.Fatalf("restore: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "3 replaced spans") {
		t.Fatalf("want three spans including the MultiEdit sub-edits, got:\n%s", out)
	}

	// Newest first, so the MultiEdit spans lead and the function body is last.
	out, err = captureRun(t, "restore", "retry.go", "--span", "3")
	if err != nil {
		t.Fatalf("restore --span: %v", err)
	}
	if !strings.Contains(out, "func retry() error {") || !strings.Contains(out, "return nil") {
		t.Fatalf("span should come back byte for byte, got:\n%s", out)
	}

	dst := filepath.Join(t.TempDir(), "recovered.txt")
	out, err = captureRun(t, "restore", "retry.go", "--span", "3", "-o", dst)
	if err != nil {
		t.Fatalf("restore -o: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "func retry() error {\n\treturn nil\n}" {
		t.Fatalf("recovered file = %q", body)
	}
	if !strings.Contains(out, "not the file") {
		t.Fatalf("output must say what it is handing back, got %q", out)
	}
}

func TestRestoreRefusesToWriteOverTheOriginal(t *testing.T) {
	writeEditFixture(t)
	// Same path in and out: this command exists because something overwrote
	// work, so it must not do the same.
	err := runRestore(os.Getenv("DEJA_INDEX_DIR"), []string{"retry.go", "--span", "1", "-o", "retry.go"}, os.Stdout)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("want a refusal, got %v", err)
	}
}

func TestRestoreUnknownPathAndBadArgs(t *testing.T) {
	writeEditFixture(t)
	out, err := captureRun(t, "restore", "nothing-here.go")
	if err != nil || !strings.Contains(out, "no replaced spans") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if err := runRestore(t.TempDir(), nil, os.Stdout); err == nil {
		t.Fatal("no path should be a usage error")
	}
	if err := runRestore(t.TempDir(), []string{"a.go", "--span", "x"}, os.Stdout); err == nil {
		t.Fatal("--span wants a number")
	}
	if err := runRestore(os.Getenv("DEJA_INDEX_DIR"), []string{"retry.go", "--span", "99"}, os.Stdout); err == nil {
		t.Fatal("out of range span should fail")
	}
}

func TestPathMatches(t *testing.T) {
	if !pathMatches("/w/pkg/retry.go", "retry.go") {
		t.Error("a bare file name should match")
	}
	if !pathMatches("/w/pkg/retry.go", "pkg/retry.go") {
		t.Error("a path suffix should match")
	}
	if !pathMatches("/w/pkg/retry.go", "/w/pkg/retry.go") {
		t.Error("the whole path should match")
	}
	if pathMatches("/w/pkg/retry.go", "other.go") {
		t.Error("a different file should not match")
	}
	if pathMatches("/w/pkgretry.go", "retry.go") {
		t.Error("a suffix that is not a path segment should not match")
	}
}

func TestRedactionNote(t *testing.T) {
	if redactionNote("token = [redacted:credential]") == "" {
		t.Error("a redacted span must be flagged, restoring it is not byte-exact")
	}
	if redactionNote("func x() {}") != "" {
		t.Error("clean spans carry no note")
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("abcdefghijklmnop"); got != "abcdefghijkl" {
		t.Fatalf("got %q", got)
	}
	if got := shortID("short"); got != "short" {
		t.Fatalf("got %q", got)
	}
}
