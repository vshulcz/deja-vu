package main

import (
	"strings"
	"testing"
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
