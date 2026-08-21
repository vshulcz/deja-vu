package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A generated layer holds the empty array. A list entry cannot follow it, so
// the literal has to go — otherwise dsh refuses the whole profile and the only
// symptom is a harness that will not boot.
func TestDeepSeekPatchReplacesTheEmptyArray(t *testing.T) {
	got := dshPatchWith("# a comment\n[]\n", dshPatchBlock("/usr/local/bin/deja"))
	if strings.Contains(got, "[]") {
		t.Errorf("the empty-array literal survived:\n%s", got)
	}
	if !strings.Contains(got, "# a comment") {
		t.Errorf("the user's own line was dropped:\n%s", got)
	}
	// An insert list, not a bare row: a patch entry addresses a row that
	// already exists, and dsh answers `entry "mcp-deja" not found` for one that
	// does not.
	if !strings.Contains(got, "- insert:") {
		t.Errorf("the block is not an insert list:\n%s", got)
	}
	if !strings.Contains(got, "@deepseek-ai/dsh-mcp-client") {
		t.Errorf("the block names no plugin:\n%s", got)
	}
}

func TestDeepSeekPatchIsRewrittenNotRepeated(t *testing.T) {
	first := dshPatchWith("[]\n", dshPatchBlock("/old/deja"))
	second := dshPatchWith(first, dshPatchBlock("/new/deja"))
	if strings.Count(second, dshBlockStart) != 1 {
		t.Errorf("the block was added twice:\n%s", second)
	}
	if strings.Contains(second, "/old/deja") {
		t.Errorf("the previous path survived a re-install:\n%s", second)
	}
}

func TestInstallDeepSeekWritesTheHomeLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	r, err := installDeepSeekMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".dsh", "cordis.patch.yml"); r.Path != want {
		t.Fatalf("wrote %q, want the home layer %q", r.Path, want)
	}
	body, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "serverName: deja") {
		t.Errorf("layer does not configure the server:\n%s", body)
	}

	// Uninstall leaves the file loadable rather than empty: a patch file with
	// nothing in it fails the profile load.
	if _, err := installDeepSeekMCP("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "mcp-deja") {
		t.Errorf("uninstall left the entry behind:\n%s", body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("uninstall left %q, want the empty array", strings.TrimSpace(string(body)))
	}
}

// Uninstalling on a machine that never had deja must not create the file.
func TestUninstallDeepSeekWithoutALayerWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	r, err := installDeepSeekMCP("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", r.Action)
	}
	if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
		t.Errorf("uninstall created %s", r.Path)
	}
}
