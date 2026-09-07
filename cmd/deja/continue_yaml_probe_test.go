package main

import (
	"strings"
	"testing"
)

// A probe on the two string helpers, so a failure says which one moved the
// text rather than only that the file came out wrong.
func TestContinueYAMLHelpers(t *testing.T) {
	doc := "name: mine\nmcpServers:\n  - name: sqlite\n    command: uvx\nrules:\n  - be brief\n"
	got := appendYAMLListItem(doc, "mcpServers", "  - name: deja\n    command: /bin/deja\n")
	want := "name: mine\nmcpServers:\n  - name: sqlite\n    command: uvx\n  - name: deja\n    command: /bin/deja\nrules:\n  - be brief\n"
	if got != want {
		t.Fatalf("append put the item in the wrong place:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	back := removeContinueItem(got, "mcpServers", "deja")
	if back != doc {
		t.Fatalf("remove did not put the file back:\n--- got ---\n%q\n--- want ---\n%q", back, doc)
	}
	// A file with no such key gets one.
	fresh := appendYAMLListItem("name: mine\n", "prompts", "  - name: deja\n")
	if fresh != "name: mine\nprompts:\n  - name: deja\n" {
		t.Fatalf("append did not write the missing key:\n%q", fresh)
	}
	// And the key at the end of the file, with no line after it.
	tail := appendYAMLListItem("name: mine\nprompts:\n  - name: other\n", "prompts", "  - name: deja\n")
	if !strings.HasSuffix(tail, "  - name: other\n  - name: deja\n") {
		t.Fatalf("append at the end of the file:\n%q", tail)
	}
}
