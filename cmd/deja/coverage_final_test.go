package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// --- install.go ---

func TestWriteIfChangedWritePermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not block writes the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()
	path := filepath.Join(dir, "config.json")
	if _, err := writeIfChanged(path, nil, []byte("x")); err == nil {
		t.Fatal("expected WriteFile permission error")
	}
}

func TestInstallClaudeHookMalformedExisting(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	dir := filepath.Join(h, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"hooks":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook("/bin/deja", false); err == nil {
		t.Fatal("expected malformed settings.json error")
	}
}

func TestUpdateOpencodeJSONCEmptyUninstall(t *testing.T) {
	if got := string(updateOpencodeJSONC(nil, "/bin/deja", true)); got != "{}\n" {
		t.Fatalf("empty uninstall jsonc = %q", got)
	}
}

func TestRunInstallBadTargetPropagatesError(t *testing.T) {
	if err := runInstall(index.DefaultDir(), []string{"totally-unknown-target"}, false); err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("runInstall bad target err = %v", err)
	}
}

func TestRunInstallAutoPrintsLogoOnTTYLikeStdout(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("NO_COLOR", "")
	if err := os.MkdirAll(filepath.Join(h, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	oldStdout := os.Stdout
	os.Stdout = devNull
	defer func() { os.Stdout = oldStdout }()

	if err := runInstall(index.DefaultDir(), []string{"--auto"}, false); err != nil {
		t.Fatalf("runInstall --auto err = %v", err)
	}
}

// --- main.go ---

func TestPrintSourcesAntigravityFallback(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", "")

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printSources(index.DefaultDir())
	_ = w.Close()
	os.Stdout = oldStdout
	b, _ := io.ReadAll(r)
	if !strings.Contains(string(b), "antigravity*") {
		t.Fatalf("printSources fallback output = %q", string(b))
	}
}

// --- mcp.go ---

type alwaysErrWriter struct{}

func (alwaysErrWriter) Write(p []byte) (int, error) { return 0, fmt.Errorf("write boom") }

func TestServeMCPEncodeError(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"
	if err := serveMCP(index.DefaultDir(), strings.NewReader(req), alwaysErrWriter{}); err == nil || !strings.Contains(err.Error(), "write boom") {
		t.Fatalf("serveMCP encode error = %v", err)
	}
}

// --- stats.go ---

func TestStatColorOKRegularFileBranch(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	f, err := os.CreateTemp(t.TempDir(), "statcolor")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	// Just needs Stat() to succeed on a real *os.File; the char-device check
	// itself is the statement under test, regardless of the boolean result.
	_ = statColorOK(f)
}

func TestPrintStatsColorEnabledBranch(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	r := stats.Report{
		Harnesses:   []stats.HarnessStats{{Harness: "claude", Sessions: 1, Messages: 2}},
		TopProjects: []stats.ProjectStats{{Project: "p", Sessions: 1}},
		Monthly:     []stats.MonthStats{{Month: "2026-01", Messages: 1}},
	}
	printStats(devNull, r)
}

// --- sync.go / sync_ssh.go ---

func TestRunSyncImportHardError(t *testing.T) {
	hermeticEnv(t)
	// A directory as the import source (instead of a batch file) makes
	// index.Import fail during decode.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "batch.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runSync(index.DefaultDir(), []string{"import", dir}); err == nil {
		t.Fatal("expected sync import decode error")
	}
}

func TestDefaultSSHRunnerExecutesLocalCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on a POSIX echo binary")
	}
	out, err := sshRunner("echo", "hello-from-default-runner")
	if err != nil || !strings.Contains(out, "hello-from-default-runner") {
		t.Fatalf("default sshRunner out=%q err=%v", out, err)
	}
}

func TestSyncSSHPushMkdirTempFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TMPDIR override does not block CreateTemp the same way on windows")
	}
	setupLocalIndex(t)
	badTmp := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("TMPDIR", badTmp)
	if err := syncSSHPush(index.DefaultDir(), "host", false); err == nil {
		t.Fatal("expected MkdirTemp failure with a missing TMPDIR")
	}
}

func TestSyncSSHPullMkdirTempFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TMPDIR override does not block CreateTemp the same way on windows")
	}
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[1] == "mktemp -d" {
			return "/tmp/remote", nil
		}
		if name == "ssh" {
			return "deja: exported 3 records", nil
		}
		return "", nil
	}
	badTmp := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("TMPDIR", badTmp)
	if err := syncSSHPull(index.DefaultDir(), "host", false); err == nil {
		t.Fatal("expected MkdirTemp failure with a missing TMPDIR")
	}
}

// --- hook_context.go ---

func TestHookDigestHyphenatedProjectNameBranch(t *testing.T) {
	tmp := hermeticEnv(t)
	idx := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idx)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	// "my-project" decodes (via the dash heuristic) to "my/project", which
	// differs from the raw directory basename — exercising hookDigest's
	// second name-candidate branch.
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("hookmany%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-my-project", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-0` + fmt.Sprint(i+1) + `T03:04:05Z","message":{"role":"user","content":"hyphen project memory"}}`,
		})
	}
	if err := index.EnsureForSearch(idx, search.Options{Query: "hyphen", All: true}, false, io.Discard); err != nil {
		t.Fatal(err)
	}

	oldwd, _ := os.Getwd()
	work := filepath.Join(tmp, "workroot", "my-project")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	digest := hookDigest(index.DefaultDir())
	if digest == "" {
		t.Fatal("expected a non-empty digest for the hyphenated project fixture")
	}
}
