package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The MCP surface stopped being six tools in #1298 and became one tool with a
// mode. Seven documents went on describing six tools — the architecture page
// listed them under a heading that said `tools/list`, the Zed extension's table
// promised six entries in the agent panel where a reader would see one, and the
// Gemini extension's own guidance named `recall_context` as a tool to call.
//
// So the rule is: a document that enumerates the surface says it is one tool,
// and names the modes the tool actually declares. The modes come from
// dejaTool() rather than a list here, which is what makes this a check rather
// than a second copy.
func TestDocsDescribingTheMCPSurfaceNameTheModesItDeclares(t *testing.T) {
	root := filepath.Join("..", "..")

	schema, ok := dejaTool()["inputSchema"].(map[string]any)
	if !ok {
		t.Fatal("the tool has no inputSchema; this test cannot read its modes")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("the schema has no properties")
	}
	mode, ok := props["mode"].(map[string]any)
	if !ok {
		t.Fatal("the schema no longer takes a mode — the surface changed shape and this rule with it")
	}
	modes, ok := mode["enum"].([]string)
	if !ok || len(modes) < 2 {
		t.Fatalf("mode enum is %v; expected the list of capabilities", mode["enum"])
	}

	// Documents that list the capabilities one by one, so a mode the tool
	// gained or lost has to reach them.
	enumerating := []string{
		"README.md",
		"llms-install.md",
		"docs/ARCHITECTURE.md",
		"extensions/zed/README.md",
		"extensions/kimi/README.md",
		"extensions/grok/README.md",
	}
	// Documents that describe the shape without the list. GEMINI.md is loaded
	// into every Gemini session and says so: the detail stays in the tool
	// description, which is the only copy that cannot go stale.
	shapeOnly := []string{
		"GEMINI.md",
		"docs/guide/agents.html",
	}

	read := func(f string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			return "", false
		}
		return strings.ToLower(string(b)), true
	}
	saysOneTool := func(f, text string) {
		// Backticks vary between copies — "one tool, `deja`" and "one `deja`
		// tool" are the same sentence — so they come out for this phrase.
		plain := strings.ReplaceAll(text, "`", "")
		if !strings.Contains(plain, "one tool") && !strings.Contains(plain, "one deja tool") &&
			!strings.Contains(plain, "one mcp tool") {
			t.Errorf("%s describes the MCP server without saying it is one tool", f)
		}
	}

	for _, f := range enumerating {
		text, ok := read(f)
		if !ok {
			continue
		}
		saysOneTool(f, text)
		for _, m := range modes {
			// As code, not as prose: `how` and `fix` are ordinary words, and a
			// page dropping the `how` bullet still says "how" a dozen times.
			if !strings.Contains(text, "`"+m+"`") {
				t.Errorf("%s never names the %q mode, which the tool declares", f, m)
			}
		}
	}
	for _, f := range shapeOnly {
		text, ok := read(f)
		if !ok {
			continue
		}
		saysOneTool(f, text)
	}
}
