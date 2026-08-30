package main

import (
	"strings"
	"testing"
)

// A .jsonc config that only mentions deja in a comment has no deja entry to
// replace. The drop loop matched the word anywhere on the line, so it deleted
// the comment — and with a block comment it deleted the opening line and left
// the closing */ behind, which costs the reader every server in the file
// (#2473).
func TestACommentThatMentionsDejaSurvivesTheInstall(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		keep string
	}{
		{
			name: "a line comment",
			old: `{
  "mcp": {
    // "deja": {"type":"local","command":["/old/deja","mcp"]},
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  }
}`,
			keep: `// "deja"`,
		},
		{
			name: "a block comment",
			old: `{
  "mcp": {
    /* the "deja" server is installed by ` + "`deja install`" + `
       do not edit this block by hand */
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  }
}`,
			keep: "/* the \"deja\" server",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, note, err := updateOpencodeJSONC([]byte(tc.old), "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if !strings.Contains(got, tc.keep) {
				t.Errorf("the comment is gone:\n%s", got)
			}
			if !strings.Contains(got, `"mine"`) {
				t.Errorf("the server the comment sat above is gone:\n%s", got)
			}
			if !strings.Contains(got, `"deja": {"type":"local","command":["/usr/local/bin/deja","mcp"]}`) {
				t.Errorf("deja was not added:\n%s", got)
			}
			if note != "" {
				t.Errorf("nothing was replaced, but install says %q", note)
			}
		})
	}
}
