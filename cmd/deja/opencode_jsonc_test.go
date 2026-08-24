package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// stripJSONC removes what a .jsonc reader ignores — line comments, block
// comments and trailing commas — respecting string literals. Written out here
// rather than reusing the production helper: a test that shares the code it is
// checking would hide a bug in it.
func stripJSONC(text string) string {
	var b strings.Builder
	inString, escaped, inLine, inBlock := false, false, false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(text) && text[i+1] == '/' {
				inBlock, i = false, i+1
			}
		case inString:
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			inLine, i = true, i+1
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			inBlock, i = true, i+1
		default:
			b.WriteByte(c)
		}
	}
	return regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(b.String(), "$1")
}

// parsesAsJSONC reports whether text is JSON once a .jsonc reader's liberties
// are taken away.
func parsesAsJSONC(t *testing.T, text string) error {
	t.Helper()
	var v any
	return json.Unmarshal([]byte(stripJSONC(text)), &v)
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
		"block comment last in the block": `{
  "mcp": {
    "mine": {"type": "local", "command": ["my-server"]},
    /* a note
       over several lines
    */
  }
}`,
		"trailing comment on the last entry": `{
  "mcp": {
    "mine": {"type": "local", "command": ["my-server"]} // my server
  }
}`,
		"a // inside a string": `{
  "mcp": {
    "mine": {"type": "remote", "url": "https://example.com/my-server"}
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
			if strings.Contains(got, "*/,") || strings.Contains(got, "spanning,") {
				t.Errorf("a comma landed inside a block comment:\n%s", got)
			}
			// A comma written after a trailing comment goes with the comment,
			// and the two entries lose their separator.
			for _, l := range strings.Split(got, "\n") {
				if i := strings.Index(l, "//"); i >= 0 && !strings.Contains(l[:i], `"`) && strings.HasSuffix(strings.TrimSpace(l), ",") {
					t.Errorf("a comma landed inside a line comment: %q\n%s", l, got)
				}
			}
			// Only where the fixture had one: the empty-block case has none.
			if strings.Contains(in, "my-server") && !strings.Contains(got, "my-server") {
				t.Errorf("the user's own server is gone:\n%s", got)
			}
		})
	}
}
