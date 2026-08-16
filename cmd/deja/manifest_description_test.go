package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// registryDescriptionLimit is what the MCP registry accepts. Over it, publishing
// fails with a 422 after the release has already been tagged and built — the
// registry job runs on the merge, so nothing before it can catch this.
//
// It did: 0.17.2 went out with a description of 106 characters, added when the
// harness count was written into it, and the registry rejected it. The mcpb
// manifest was sitting on exactly 100 at the same time, which is not a margin.
const registryDescriptionLimit = 100

func TestManifestDescriptionsFitTheRegistry(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "server.json"),
		filepath.Join("..", "..", "packaging", "mcpb", "manifest.json"),
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(b, &manifest); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if manifest.Description == "" {
			t.Errorf("%s has no description", path)
			continue
		}
		if n := len(manifest.Description); n > registryDescriptionLimit {
			t.Errorf("%s description is %d characters, over the registry's %d:\n  %q",
				path, n, registryDescriptionLimit, manifest.Description)
		}
	}
}
