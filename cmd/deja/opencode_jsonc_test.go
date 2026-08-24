package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

var (
	jsoncComment = regexp.MustCompile(`//[^\n]*`)
	jsoncComma   = regexp.MustCompile(`,(\s*[}\]])`)
)

// parsesAsJSONC reports whether text survives the two liberties a .jsonc reader
// takes — line comments and trailing commas — and is JSON underneath.
func parsesAsJSONC(t *testing.T, text string) error {
	t.Helper()
	stripped := jsoncComma.ReplaceAllString(jsoncComment.ReplaceAllString(text, ""), "$1")
	var v any
	return json.Unmarshal([]byte(stripped), &v)
}

// The comma that joins deja's entry to the previous one was placed by walking
// backwards to the first line not already ending in a comma. In a file that
// uses trailing commas — which is what .jsonc is for — that scan runs past the
// last entry and lands on the line opening it, producing `"mine": {,` (#1695).
func TestOpencodeJSONCKeepsTrailingCommasValid(t *testing.T) {
	cases := map[string]string{
		"trailing commas": `{
  // my opencode config
  "theme": "system",
  "mcp": {
    "mine": {
      "type": "local",
      "command": ["my-server"],
    },
  },
}`,
		"no trailing comma": `{
  "mcp": {
    "mine": {
      "type": "local",
      "command": ["my-server"]
    }
  }
}`,
		"empty mcp block": `{
  "mcp": {
  }
}`,
		"comment last in the block": `{
  "mcp": {
    "mine": {"type": "local", "command": ["my-server"]}
    // more to come
  }
}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := updateOpencodeJSONC([]byte(in+"\n"), "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if err := parsesAsJSONC(t, got); err != nil {
				t.Errorf("install left a file that does not parse: %v\n%s", err, got)
			}
			if !strings.Contains(got, `"deja"`) {
				t.Errorf("deja's entry is missing:\n%s", got)
			}
			if strings.Contains(got, "{,") {
				t.Errorf("a comma landed on an opening brace:\n%s", got)
			}
			// Only where the fixture had one: the empty-block case has none.
			if strings.Contains(in, "my-server") && !strings.Contains(got, "my-server") {
				t.Errorf("the user's own server is gone:\n%s", got)
			}
		})
	}
}
