package main

import (
	"strings"
	"testing"
)

// Install used to put its entry last, which meant the comma joining it to the
// block went on the reader's own line — and uninstall, which cannot tell that
// comma from one they wrote, left it there (#2617).
func TestOpencodeJSONCGivesTheConfigBackAsItWas(t *testing.T) {
	cases := map[string]string{
		"one neighbour": `{
  // mine
  "mcp": {
    "theirs": { "type": "local" }
  }
}
`,
		"two neighbours": `{
  "mcp": {
    "a": { "type": "local" },
    "b": { "type": "local" }
  }
}
`,
		"their own trailing comma": `{
  "mcp": {
    "theirs": { "type": "local" },
  }
}
`,
		"neighbour over several lines": `{
  "mcp": {
    "theirs": {
      "type": "local"
    }
  }
}
`,
		"a comment above the first entry": `{
  "mcp": {
    // the one I actually use
    "theirs": { "type": "local" }
  }
}
`,
		"comments only": `{
  "mcp": {
    // nothing here yet
  }
}
`,
	}
	for name, before := range cases {
		t.Run(name, func(t *testing.T) {
			installed, _, err := updateOpencodeJSONC([]byte(before), "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(installed), `"deja"`) {
				t.Fatalf("install wrote no entry:\n%s", installed)
			}
			if err := parsesAsJSONC(t, string(installed)); err != nil {
				t.Fatalf("install left a file that does not parse: %v\n%s", err, installed)
			}
			back, _, err := updateOpencodeJSONC(installed, "/usr/local/bin/deja", true)
			if err != nil {
				t.Fatal(err)
			}
			if string(back) != before {
				t.Fatalf("uninstall did not give the config back as it was:\nwant:\n%s\ngot:\n%s", before, back)
			}
		})
	}
}

// A comment the reader put above their first entry belongs to that entry, so
// our own goes under it rather than between the two.
func TestOpencodeJSONCLeavesALeadingCommentWhereItWas(t *testing.T) {
	before := `{
  "mcp": {
    // the one I actually use
    "theirs": { "type": "local" }
  }
}
`
	out, _, err := updateOpencodeJSONC([]byte(before), "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(out), "\n")
	comment, deja := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "the one I actually use") {
			comment = i
		}
		if strings.Contains(l, `"deja"`) {
			deja = i
		}
	}
	if comment < 0 || deja < 0 || comment > deja {
		t.Fatalf("our entry went above the reader's comment:\n%s", out)
	}
}
