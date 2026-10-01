package main

import (
	"encoding/json"
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

	// A comment the reader wrote on deja's own entry stays on it when the
	// entry is rewritten, rather than moving to the next server.
	ownOld := "{\n  \"mcp\": {\n    // deja: my memory\n    \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]},\n    \"a\": {\"type\":\"local\"}\n  }\n}\n"
	next, _, err = updateOpencodeJSONC([]byte(ownOld), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(next), "// deja: my memory\n    \"deja\"") {
		t.Fatalf("the comment on deja's entry moved off it:\n%s", next)
	}

	// The first code line starts inside a block comment that closes on it:
	// the entry goes above the comment, where a parser reads it.
	inOld := "{\n  \"mcp\": {\n    /* start\n    end */ \"a\": {\"type\":\"local\"}\n  }\n}\n"
	next, _, err = updateOpencodeJSONC([]byte(inOld), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(string(next))), &root); err != nil {
		t.Fatalf("%v:\n%s", err, next)
	}
	if mcp, _ := root["mcp"].(map[string]any); mcp["deja"] == nil {
		t.Fatalf("deja's entry went into a comment:\n%s", next)
	}
}
