package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Writing into a commented config whose mcp block is empty must leave a file a
// parser still reads. A comma after the only entry made it invalid JSON, and
// every later run then refused, blaming the reader's comment for deja's byte
// (#2742).
func TestJSONCEmptyBlockStaysParseable(t *testing.T) {
	seed := "{ // hi\n  \"mcp\": {}\n}\n"
	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "deja") {
		t.Fatalf("the entry was not written:\n%s", got)
	}
	if !strings.Contains(got, "// hi") {
		t.Errorf("the reader's comment was lost:\n%s", got)
	}
	// Strip the line comments the way a jsonc reader would, then parse.
	var body []string
	for _, l := range strings.Split(got, "\n") {
		if i := strings.Index(l, "//"); i >= 0 {
			l = l[:i]
		}
		body = append(body, l)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(strings.Join(body, "\n")), &root); err != nil {
		t.Fatalf("deja left the config unparseable (%v):\n%s", err, got)
	}
	mcp, _ := root["mcp"].(map[string]any)
	if _, ok := mcp["deja"]; !ok {
		t.Errorf("the entry is not in the mcp block:\n%s", got)
	}
}
