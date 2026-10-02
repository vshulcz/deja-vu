//go:build windows

package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Claude Code on Windows encodes the drive root as well, so a project dir is
// named "C--Users-x-app", not "-Users-x-app". Before this was handled,
// resolveEncodedPath bailed on the missing "-" prefix and the caller fell
// back to the dash heuristic, which turns "domain-manage" into
// "domain\manage" and splits one project across two display names.
func TestResolveEncodedPathWindowsDriveLetter(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "Downloads", "domain-manage")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	vol := filepath.VolumeName(target) // "C:"
	if vol == "" {
		t.Skip("target has no volume name")
	}
	trimmed := strings.TrimPrefix(target, vol)
	encoded := strings.TrimSuffix(vol, ":") + "-" +
		strings.ReplaceAll(strings.ReplaceAll(trimmed, "\\", "-"), "/", "-")

	got := resolveEncodedPath(encoded)
	if got == "" {
		t.Fatalf("resolveEncodedPath(%q) = \"\" — drive-letter form not resolved", encoded)
	}
	if !strings.EqualFold(got, target) {
		t.Fatalf("resolveEncodedPath(%q) = %q, want %q", encoded, got, target)
	}
}

// The hyphenated leaf must survive: "domain-manage" is one directory, not
// "domain/manage".
func TestClaudeProjectNameWindowsKeepsHyphenatedLeaf(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "Downloads", "domain-manage")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	vol := filepath.VolumeName(target)
	if vol == "" {
		t.Skip("target has no volume name")
	}
	trimmed := strings.TrimPrefix(target, vol)
	encoded := strings.TrimSuffix(vol, ":") + "-" +
		strings.ReplaceAll(strings.ReplaceAll(trimmed, "\\", "-"), "/", "-")

	got := decodeProjectBase(encoded)
	if !strings.Contains(got, "domain-manage") {
		t.Fatalf("decodeProjectBase(%q) = %q, want it to keep \"domain-manage\"", encoded, got)
	}
}

// A checkout at a drive root, X:\proj, is "proj" whichever way a Claude Code
// session gets its name: from the files it edited, from the folder decoded
// against the disk, or from its cwd. subst gives the test a drive of its own.
func TestADriveRootRepoIsOneProjectOnWindows(t *testing.T) {
	dir := t.TempDir()
	drive := ""
	for c := 'Z'; c >= 'M'; c-- {
		d := string(c) + ":"
		if _, err := os.Stat(d + `\`); err != nil && exec.Command("subst", d, dir).Run() == nil {
			drive = d
			break
		}
	}
	if drive == "" {
		t.Skip("no free drive letter for subst")
	}
	t.Cleanup(func() { _ = exec.Command("subst", drive, "/D").Run() })
	repo := drive + `\proj`
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := func(n string) string { return filepath.Join(repo, n) }
	ms := []model.Message{filesMsg(f("a.go"), f("b.go"), f("c.go"))}
	if got := projectFromPaths(ms); got != "proj" {
		t.Errorf("files edited in %s name %q, want proj", repo, got)
	}
	if got := decodeProjectBase(claudeEncodePath(repo)); got != "proj" {
		t.Errorf("folder %s decodes to %q, want proj", claudeEncodePath(repo), got)
	}
}
