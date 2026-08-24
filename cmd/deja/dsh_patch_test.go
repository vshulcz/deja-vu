package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

const dshDejaBlock = dshBlockStart + `
- insert:
    - id: mcp-deja
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: deja
` + dshBlockEnd

const dshUserPatch = `# my own patch
- insert:
    - id: my-plugin
      name: "my-plugin.js"`

// The block's own first line says "safe to delete", and deleting half of it
// cost the user the patch they had written below: the skip ran to end of file
// (#1701).
func TestDSHPatchRefusesAHalfMarkedBlock(t *testing.T) {
	noEnd := strings.ReplaceAll(dshDejaBlock, dshBlockEnd, "") + "\n" + dshUserPatch
	if _, err := dshPatchWith(noEnd, "BLOCK"); err == nil {
		t.Error("a block with no end marker was edited anyway")
	}
	noStart := strings.ReplaceAll(dshDejaBlock, dshBlockStart, "")
	if _, err := dshPatchWith(noStart, "BLOCK"); err == nil {
		t.Error("a block with no start marker was edited anyway")
	}
	// Both markers, in the wrong order: the skip still runs to end of file.
	wrongOrder := dshBlockEnd + "\n- insert:\n    - id: mcp-deja\n" + dshBlockStart + "\n" + dshUserPatch
	if _, err := dshPatchWith(wrongOrder, "BLOCK"); err == nil {
		t.Error("a block whose end marker comes first was edited anyway")
	}
	// A marker quoted inside a value is not a marker.
	quoted := dshUserPatch + "\n    - id: note\n      name: \"" + dshBlockEnd + "\"\n"
	if _, err := dshPatchWith(quoted, "BLOCK"); err != nil {
		t.Errorf("a marker quoted inside a value was taken for one: %v", err)
	}
}

// Uninstall is exactly when someone with a hand-edited file needs their own
// content left alone, and it called stripDSHBlock with no check at all.
func TestUninstallDeepSeekRefusesAHalfMarkedBlock(t *testing.T) {
	hermeticEnv(t)
	dir := sources.DSHHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cordis.patch.yml")
	half := strings.ReplaceAll(dshDejaBlock, dshBlockEnd, "") + "\n" + dshUserPatch + "\n"
	if err := os.WriteFile(path, []byte(half), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "deepseek"); err == nil {
		t.Error("uninstall edited a file it could not bound")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != half {
		t.Errorf("uninstall changed the file:\n%s", after)
	}
	if !strings.Contains(string(after), "my-plugin") {
		t.Errorf("uninstall deleted the user's patch:\n%s", after)
	}
}

// The control: both markers present, and no deja block at all, are both edited
// as before — the user's patch kept, deja's block replaced or appended once.
func TestDSHPatchKeepsTheUsersPatch(t *testing.T) {
	both := dshUserPatch + "\n" + dshDejaBlock
	got, err := dshPatchWith(both, "BLOCK")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "my-plugin") {
		t.Errorf("the user's patch was dropped:\n%s", got)
	}
	if strings.Contains(got, "mcp-deja") {
		t.Errorf("the old deja block survived:\n%s", got)
	}
	if n := strings.Count(got, "BLOCK"); n != 1 {
		t.Errorf("expected one new block, found %d:\n%s", n, got)
	}

	got, err = dshPatchWith(dshUserPatch, "BLOCK")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "my-plugin") || strings.Count(got, "BLOCK") != 1 {
		t.Errorf("a file deja had never touched came out wrong:\n%s", got)
	}
}
