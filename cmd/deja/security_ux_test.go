package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestShareEndsWithBoundaryLine(t *testing.T) {
	hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "s.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the api broke with token sk-test1234567890abcdefghijklmnop in the header"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		var out bytes.Buffer
		if err := runShare(dir, []string{"s1"}, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "review before sending") {
			t.Fatal("boundary line must go to stderr, not into the shared text")
		}
	})
	if !strings.Contains(stderr, "pattern redaction is a floor") || !strings.Contains(stderr, "rotate anything that leaked") {
		t.Fatalf("share must end with the boundary line, got:\n%s", stderr)
	}
}

func TestSyncExportPrintsBoundaryLine(t *testing.T) {
	hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "s.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"export me"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		if err := runSync(dir, []string{"export", filepath.Join(t.TempDir(), "out")}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stderr, "pattern redaction is a floor") {
		t.Fatalf("sync export must print the boundary line, got:\n%s", stderr)
	}
}

func TestDoctorStatesSecurityBoundary(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--offline"}, nil, index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plaintext on disk") || !strings.Contains(out.String(), "no encryption") {
		t.Fatalf("doctor must state the security boundary:\n%s", out.String())
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}
