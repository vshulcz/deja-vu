//go:build windows

package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
