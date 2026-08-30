package main

import (
	"strings"
	"testing"
)

// The text path recognised the literal key "deja" and nothing else, so an entry
// somebody wired by hand under another name was invisible to it: install added
// a sibling and the harness started two servers, doctor named the second, and
// uninstall left one behind without a word (#2742).

// Adopt the hand-written entry rather than add a sibling — what dejaEntryKey
// does on the parsed path (#2269).
func TestJSONCAdoptsAnEntryUnderAnotherName(t *testing.T) {
	seed := "{\n  \"mcp\": {\n" +
		"    \"deja-vu\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]}\n" +
		"  }\n}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "/bin/deja") != 1 {
		t.Fatalf("the binary was not written once:\n%s", got)
	}
	if strings.Contains(got, "/old/deja") {
		t.Errorf("the old binary is still launched:\n%s", got)
	}
	if strings.Contains(got, `"deja":`) {
		t.Errorf("added a sibling beside the entry that was already here:\n%s", got)
	}
	if !strings.Contains(got, `"deja-vu"`) {
		t.Errorf("lost the name the entry was written under:\n%s", got)
	}

	// And again on its own output: an adoption that only holds the first time
	// grows a sibling on the next install instead.
	again, _, err := updateOpencodeJSONC(out, "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != got {
		t.Errorf("a second install changed the file again:\n%s", again)
	}
}

// Name the further entries that also run deja, the way every other MCP writer
// does (#2712).
func TestJSONCNamesAnotherEntryThatRunsDeja(t *testing.T) {
	seed := "{\n  \"mcp\": {\n" +
		"    \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]},\n" +
		"    \"deja-vu\": {\"type\":\"local\",\"command\":[\"/other/deja\",\"mcp\"]}\n" +
		"  }\n}\n"

	_, note, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "deja-vu") {
		t.Errorf("nothing named the second entry: %q", note)
	}
}

// Uninstall leaves an entry it did not write, which is right — but saying
// nothing looks like an uninstall that missed one (#2269).
func TestJSONCUninstallNamesWhatItLeaves(t *testing.T) {
	seed := "{\n  \"mcp\": {\n" +
		"    \"deja\": {\"type\":\"local\",\"command\":[\"/bin/deja\",\"mcp\"]},\n" +
		"    \"deja-vu\": {\"type\":\"local\",\"command\":[\"/other/deja\",\"mcp\"]}\n" +
		"  }\n}\n"

	out, note, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "deja-vu") {
		t.Fatalf("removed an entry deja did not write:\n%s", out)
	}
	if !strings.Contains(note, "deja-vu") {
		t.Errorf("nothing named the entry left behind: %q", note)
	}
}
