package main

import (
	"encoding/json"
	"testing"
)

// Gemini can install this repository as an extension directly, and `deja
// install gemini` writes an extension of its own. Both are the same product, so
// both must claim the same name: Gemini keys an extension by the name in its
// manifest and refuses a second install under a name it already has
// ("Extension "deja" is already installed"). Let the two names drift and a
// machine ends up with two deja extensions, two MCP servers and every tool
// listed twice — the fault Zed had until one shared server id made it
// impossible.
func TestGeminiExtensionSharesTheInstallerName(t *testing.T) {
	var manifest struct {
		Name        string                    `json:"name"`
		Version     string                    `json:"version"`
		Description string                    `json:"description"`
		MCPServers  map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "gemini-extension.json"), &manifest); err != nil {
		t.Fatalf("gemini-extension.json: %v", err)
	}
	if manifest.Name != geminiExtensionName {
		t.Fatalf("manifest name is %q, the installer writes %q — a machine with both gets two deja extensions", manifest.Name, geminiExtensionName)
	}
	if manifest.Version == "" || manifest.Description == "" {
		t.Fatal("the gallery reads version and description; an empty one is a listing that says nothing")
	}
	// The gallery crawler wants the manifest at the absolute root, which is why
	// this file sits beside go.mod rather than under extensions/.
	server, ok := manifest.MCPServers["deja"]
	if !ok {
		t.Fatal("the extension carries no deja MCP server, which is the only thing it is for")
	}
	if server["command"] != "deja" {
		t.Fatalf("MCP command is %v; it has to be the binary on PATH, since an extension ships files rather than binaries", server["command"])
	}
}
