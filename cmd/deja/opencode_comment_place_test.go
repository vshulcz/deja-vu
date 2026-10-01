package main

import (
	"strings"
	"testing"
)

// A comment directly above the reader's first server is about that server;
// deja's entry went between the two and the comment read as describing deja
// (#4203). The entry goes above the comment, and uninstall still gives the
// file back as it was.
func TestOpencodeEntryGoesAboveTheCommentOfTheFirstServer(t *testing.T) {
	old := `{
  "mcp": {
    // my other server
    // runs nothing
    "other": { "type": "local", "command": ["true"] },
  },
}
`
	next, _, err := updateOpencodeJSONC([]byte(old), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(next)
	if d, c := strings.Index(s, `"deja"`), strings.Index(s, "// my other server"); d < 0 || d > c {
		t.Fatalf("deja's entry sits under the reader's comment:\n%s", s)
	}
	if !strings.Contains(s, "// runs nothing\n    \"other\"") {
		t.Fatalf("the comment came apart from its server:\n%s", s)
	}
	back, _, err := updateOpencodeJSONC(next, "/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != old {
		t.Fatalf("uninstall did not give the file back:\n%s", back)
	}

	// Inside a block comment a line starting with // is not a comment of its
	// own, and the entry must not land in the middle of it.
	blockOld := "{\n  \"mcp\": {\n    /* servers\n    // old\n    */\n    \"other\": {\"type\":\"local\",\"command\":[\"true\"]}\n  }\n}\n"
	next, _, err = updateOpencodeJSONC([]byte(blockOld), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if i, j := strings.Index(string(next), `"deja"`), strings.Index(string(next), "*/"); i < j {
		t.Fatalf("deja's entry went inside a block comment:\n%s", next)
	}
}
