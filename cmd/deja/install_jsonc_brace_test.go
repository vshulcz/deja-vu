package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// jsoncValue reads a .jsonc file the way a config parser does: comments out,
// trailing commas out, then JSON. A test that only looked at the text would
// miss the case where deja's entry lands outside the "mcp" block — that file
// parses, it just does not wire deja to anything.
func jsoncValue(t *testing.T, b []byte) map[string]any {
	t.Helper()
	s := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(b), "")
	s = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(s, "$1")
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("the config no longer parses (%v):\n%s", err, b)
	}
	return m
}

// The writer bounds the "mcp" block by counting braces as text, so a brace
// inside a quoted command path counts as structure: an unmatched "{" puts
// deja's entry outside the block, an unmatched "}" puts it in without a comma,
// and one in deja's own entry makes the drop loop swallow the server under it
// (#2475). Paths like ${VENDOR} or /opt/{a,b}/bin are ordinary.
func TestABraceInAQuotedPathIsNotStructure(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		keep string
	}{
		{
			name: "an unmatched open brace",
			old: `{
  "mcp": {
    "tools": {"type":"local","command":["/opt/shells/{/bin/tools"]},
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  },
  "theme": "dark"
}`,
			keep: "tools",
		},
		{
			name: "an unmatched closing brace",
			old: `{
  "mcp": {
    "tools": {"type":"local","command":["/opt/shells/}/bin/tools"]},
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  },
  "theme": "dark"
}`,
			keep: "tools",
		},
		{
			name: "a brace in deja's own entry",
			old: `{
  "mcp": {
    "deja": {"type":"local","command":["/opt/{/deja","mcp"]},
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  }
}`,
			keep: "mine",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := updateOpencodeJSONC([]byte(tc.old), "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			got := jsoncValue(t, out)
			mcp, _ := got["mcp"].(map[string]any)
			if mcp == nil {
				t.Fatalf("the mcp block is gone:\n%s", out)
			}
			if _, ok := mcp["deja"]; !ok {
				t.Errorf("deja is not in the mcp block:\n%s", out)
			}
			if _, ok := got["deja"]; ok {
				t.Errorf("deja was written as a top-level key, where opencode will not look for it:\n%s", out)
			}
			if _, ok := mcp[tc.keep]; !ok {
				t.Errorf("the %q server the reader already had is gone:\n%s", tc.keep, out)
			}
			if strings.Contains(string(out), `"/usr/local/bin/deja"`) == false {
				t.Errorf("deja's own command is not in the file:\n%s", out)
			}
		})
	}
}
