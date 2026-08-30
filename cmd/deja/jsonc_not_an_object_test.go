package main

import (
	"strings"
	"testing"
)

// An "mcp" key that is not an object cannot hold a server. Writing a second key
// of the same name above it left both in the file; every reader takes the last,
// which is the reader's, so deja was not wired and install said "updated"
// anyway. The parsed path refuses this (#2399); the text path did not (#2742).
func TestJSONCRefusesABlockThatIsNotAnObject(t *testing.T) {
	for _, seed := range []string{
		"{ // hi\n  \"mcp\": null\n}\n",
		"{ // hi\n  \"mcp\": []\n}\n",
		"{ // hi\n  \"mcp\": \"off\"\n}\n",
	} {
		out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
		if err == nil {
			t.Errorf("install wrote into a config whose mcp key is not an object:\n%s", out)
			continue
		}
		if !strings.Contains(err.Error(), "by hand") {
			t.Errorf("the refusal does not say what to do: %v", err)
		}
	}
}
