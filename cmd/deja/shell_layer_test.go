package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestShellLayerScripts(t *testing.T) {
	for _, tc := range []struct {
		shell string
		want  string
	}{
		{"zsh", "add-zsh-hook preexec _deja_shell_preexec"},
		{"bash", "trap '_deja_shell_preexec' DEBUG"},
		{"fish", "function _deja_shell_preexec --on-event fish_preexec"},
	} {
		var out, errBuf bytes.Buffer
		if err := runShell("", []string{tc.shell}, &out, &errBuf); err != nil {
			t.Fatalf("runShell(%s): %v", tc.shell, err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("runShell(%s) missing %q in:\n%s", tc.shell, tc.want, out.String())
		}
		if !strings.Contains(out.String(), "deja shell preexec") || !strings.Contains(out.String(), "deja shell postexec") {
			t.Errorf("runShell(%s) missing preexec/postexec calls in:\n%s", tc.shell, out.String())
		}
	}

	var out, errBuf bytes.Buffer
	if err := runShell("", nil, &out, &errBuf); err == nil {
		t.Error("bare `deja shell` succeeded; want usage error")
	}
	if err := runShell("", []string{"tcsh"}, &out, &errBuf); err == nil {
		t.Error("`deja shell tcsh` succeeded; want unsupported shell error")
	}
}

func TestShellPreexecAndPostexec(t *testing.T) {
	seedFallthroughStore(t)
	dir := index.DefaultDir()
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/app")

	// Before running a command that repeatedly failed on a missing program in
	// history, `deja shell preexec` prints the pre-command warning to stderr.
	var beforeOut, beforeErr bytes.Buffer
	if err := runShell(dir, []string{"preexec", "sh-test-1", "quibtimer 30 make migrate-orders"}, &beforeOut, &beforeErr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(beforeErr.String(), "quibtimer is not on this machine") {
		t.Errorf("shell preexec did not warn about missing binary; got %q", beforeErr.String())
	}

	// Running a `deja` command itself is skipped so shell hooks never recurse.
	var skipOut, skipErr bytes.Buffer
	if err := runShell(dir, []string{"preexec", "sh-test-1", "deja status"}, &skipOut, &skipErr); err != nil {
		t.Fatal(err)
	}
	if skipErr.Len() != 0 {
		t.Errorf("shell preexec should skip deja commands; got %q", skipErr.String())
	}

	// After a failed command, `deja shell postexec` prints the known fix pair.
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir = os.Getenv("DEJA_INDEX_DIR")
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/app")
	var afterOut, afterErr bytes.Buffer
	if err := runShell(dir, []string{
		"postexec", "sh-test-2", "2", "make", "panic: sql: database is closed",
	}, &afterOut, &afterErr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(afterErr.String(), "CGO_ENABLED=0") {
		t.Errorf("shell postexec did not print fix pair; got %q", afterErr.String())
	}
}

func TestShellCommandWrapper(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/app")
	t.Setenv("DEJA_SHELL_SESSION", "sh-wrap-1")

	var stdout, stderr bytes.Buffer
	err := runShell(dir, []string{"-c", "echo 'panic: sql: database is closed' >&2; exit 2"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit from failing -c command")
	}
	if !strings.Contains(stderr.String(), "CGO_ENABLED=0") {
		t.Errorf("expected fix-pair recall on stderr after command failure; got %q", stderr.String())
	}
}

func TestShellVerifyCallThrough(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "index")
	t.Setenv("DEJA_INDEX_DIR", dir)

	if _, err := captureRun(t, "install", "claude", "--no-index"); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := runShell(dir, []string{"--verify", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("runShell --verify --json: %v", err)
	}
	var rows []shellVerifyRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal verify json: %v (%s)", err, out.String())
	}
	var found bool
	for _, r := range rows {
		if r.Target == "claude-code" {
			found = true
			if r.Status != "ok" || r.Tools == 0 {
				t.Errorf("expected claude-code status ok with tools > 0, got %+v", r)
			}
		}
	}
	if !found {
		t.Errorf("claude-code not found in verify output: %+v", rows)
	}
}
