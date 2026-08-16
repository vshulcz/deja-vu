package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The bundle manifest is what a desktop app reads when someone opens the .mcpb:
// it is the tool list those users see. It listed four while the server has
// served six since fix and how shipped, so two of them were invisible to
// everyone who installed that way — the same gap the README had, in a place
// nobody re-reads.
func TestBundleManifestListsEveryToolTheServerServes(t *testing.T) {
	resp, code, msg := handleMCP(t.TempDir(), rpcRequest{Method: "tools/list"})
	if code != 0 {
		t.Fatalf("tools/list: %d %s", code, msg)
	}
	payload, _ := resp.(map[string]any)
	served, ok := payload["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("tools is %T", payload["tools"])
	}

	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "mcpb", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}

	declared := map[string]bool{}
	for _, tool := range manifest.Tools {
		if tool.Description == "" {
			t.Errorf("the bundle declares %q with no description", tool.Name)
		}
		declared[tool.Name] = true
	}
	for _, tool := range served {
		name, _ := tool["name"].(string)
		if !declared[name] {
			t.Errorf("the server serves %q and the bundle manifest does not declare it", name)
		}
		delete(declared, name)
	}
	for name := range declared {
		t.Errorf("the bundle declares %q, which the server does not serve", name)
	}
}
