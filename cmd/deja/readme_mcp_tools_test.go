package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The README listed four MCP tools while the server has served six since fix and
// how were added. Nothing compared the two, so the page quietly undersold the
// product — Glama's own listing of this server had the right six.
//
// This reads the tools the server actually registers and requires each one to
// appear in the README, both in the sentence and in the argument table.
func TestReadmeListsEveryMCPToolTheServerRegisters(t *testing.T) {
	resp, code, msg := handleMCP(t.TempDir(), rpcRequest{Method: "tools/list"})
	if code != 0 {
		t.Fatalf("tools/list: %d %s", code, msg)
	}
	payload, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("tools/list returned %T", resp)
	}
	tools, ok := payload["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("tools is %T", payload["tools"])
	}

	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)

	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatalf("a tool has no name: %v", tool)
		}
		if !strings.Contains(readme, "`"+name+"`") {
			t.Errorf("README never mentions the %q tool", name)
		}
		if !strings.Contains(readme, "| `"+name+"` |") {
			t.Errorf("README's MCP table has no row for %q", name)
		}

		schema, _ := tool["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		row := tableRow(readme, name)
		for arg := range props {
			// the table marks optional arguments with a trailing "?" inside the
			// code span, so both spellings count as naming it
			if !strings.Contains(row, "`"+arg+"`") && !strings.Contains(row, "`"+arg+"?`") {
				t.Errorf("README's %q row does not name the %q argument: %s", name, arg, row)
			}
		}
	}
}

// tableRow returns the README table row for a tool, or "" when there is none.
func tableRow(readme, tool string) string {
	head := "| `" + tool + "` |"
	i := strings.Index(readme, head)
	if i < 0 {
		return ""
	}
	rest := readme[i:]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		return rest[:j]
	}
	return rest
}
