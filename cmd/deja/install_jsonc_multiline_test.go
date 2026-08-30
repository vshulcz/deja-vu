package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// The .jsonc writer drops the deja entry by looking for the string on a line.
// An entry written across lines carries it on the first one only, so the rest
// stayed behind and the config stopped parsing (#2394).
func TestInstallDropsAWholeMultilineEntry(t *testing.T) {
	before := `{
  // my settings
  "mcp": {
    "mine": {"type":"local","command":["/usr/bin/mine"]},
    "deja": {
      "type": "local",
      "command": ["/home/me/bin/deja-wrapper", "mcp"]
    }
  }
}
`
	next, note, err := updateOpencodeJSONC([]byte(before), "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	got := string(next)
	if !strings.Contains(got, "// my settings") {
		t.Errorf("the comment did not survive:\n%s", got)
	}
	if !strings.Contains(note, "/home/me/bin/deja-wrapper") {
		t.Errorf("the note did not name what it replaced: %q", note)
	}
	if strings.Contains(got, "deja-wrapper") {
		t.Errorf("the old entry is still in the file:\n%s", got)
	}

	var root struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	stripped := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(got, "")
	if err := json.Unmarshal([]byte(stripped), &root); err != nil {
		t.Fatalf("the config no longer parses: %v\n%s", err, got)
	}
	if len(root.MCP) != 2 || root.MCP["mine"] == nil || root.MCP["deja"] == nil {
		t.Errorf("the block should hold their server and deja's, got %d entries:\n%s", len(root.MCP), got)
	}
}
