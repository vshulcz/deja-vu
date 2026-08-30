package main

import (
	"strings"
	"testing"
)

// The line that says deja was left switched off was keyed to one spelling.
// opencode writes `enabled: false`, and a config carrying that reported a
// clean install for wiring that will never run (#2757).
func TestAnEntrySwitchedOffIsReportedWhicheverWayItIsSpelt(t *testing.T) {
	for _, off := range []map[string]any{
		{"disabled": true},
		{"enabled": false},
	} {
		name := "disabled"
		if _, ok := off["enabled"]; ok {
			name = "enabled false"
		}
		t.Run(name, func(t *testing.T) {
			prev := map[string]any{"command": "/old/deja", "args": []any{"mcp"}}
			for k, v := range off {
				prev[k] = v
			}
			merged, note := mergeDejaEntry(prev, map[string]any{"command": "/new/deja", "args": []string{"mcp"}})
			for k, v := range off {
				if merged[k] != v {
					t.Errorf("the switch was not carried through: %v", merged)
				}
			}
			if !strings.Contains(note, "switched off") {
				t.Errorf("the reader is not told the entry is off: %q", note)
			}
		})
	}
}

// And an entry that is on says nothing, whichever way that is spelt.
func TestAnEntryThatIsOnSaysNothingAboutASwitch(t *testing.T) {
	for _, on := range []map[string]any{
		{},
		{"disabled": false},
		{"enabled": true},
	} {
		prev := map[string]any{"command": "/old/deja", "args": []any{"mcp"}}
		for k, v := range on {
			prev[k] = v
		}
		_, note := mergeDejaEntry(prev, map[string]any{"command": "/new/deja", "args": []string{"mcp"}})
		if strings.Contains(note, "switched off") {
			t.Errorf("%v: an entry that is on was reported as off: %q", on, note)
		}
	}
}

// opencode.jsonc is written by a second writer that rewrites deja's entry as
// one line, so a switch the reader had set went out with the old line and the
// entry came back on with install reporting success (#2757).
func TestTheOpencodeJSONCWriterKeepsASwitchTheReaderSet(t *testing.T) {
	for _, sw := range []string{`"enabled":false`, `"disabled":true`} {
		t.Run(sw, func(t *testing.T) {
			old := "{\n  \"mcp\": {\n    \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]," + sw + "}\n  }\n}\n"
			next, note, err := updateOpencodeJSONC([]byte(old), "/new/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(next), sw) {
				t.Errorf("the switch was dropped:\n%s", next)
			}
			if !strings.Contains(string(next), "/new/deja") {
				t.Errorf("the entry was not updated:\n%s", next)
			}
			if !strings.Contains(note, "switched off") {
				t.Errorf("the reader is not told the entry is off: %q", note)
			}
		})
	}
}

// An entry that is on keeps its shape and says nothing.
func TestTheOpencodeJSONCWriterSaysNothingAboutAnEntryThatIsOn(t *testing.T) {
	for _, on := range []string{"", `,"enabled":true`, `,"disabled":false`} {
		old := "{\n  \"mcp\": {\n    \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]" + on + "}\n  }\n}\n"
		next, note, err := updateOpencodeJSONC([]byte(old), "/new/deja", false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(note, "switched off") {
			t.Errorf("%q: an entry that is on was reported as off: %q", on, note)
		}
		if strings.Contains(string(next), "\"enabled\":false") || strings.Contains(string(next), "\"disabled\":true") {
			t.Errorf("%q: an entry that is on was written off:\n%s", on, next)
		}
	}
}

// The switch is read out of the entry, not out of the reader's words about it
// and not out of a block nested inside it: both switched a running server off
// and said deja had left it as it was.
func TestTheOpencodeJSONCWriterReadsTheEntrysOwnSwitch(t *testing.T) {
	for _, entry := range []string{
		`"deja": {"type":"local","command":["/old/deja","mcp"]} // "enabled": false when I travel`,
		`"deja": {"type":"local","command":["/old/deja","mcp"]} /* "disabled": true once */`,
		`"deja": {"type":"local","command":["/old/deja","mcp"],"options":{"enabled":false},"enabled":true}`,
		`"deja": {"type":"local","command":["/old/deja","mcp"],"enabled":"false"}`,
		// A comment inside the entry, at the depth its own keys sit at.
		"\"deja\": {\"type\":\"local\", // \"enabled\": false when I travel\n      \"command\":[\"/old/deja\",\"mcp\"]}",
		"\"deja\": {\"type\":\"local\", /* \"disabled\": true once */ \"command\":[\"/old/deja\",\"mcp\"]}",
	} {
		old := "{\n  \"mcp\": {\n    " + entry + "\n  }\n}\n"
		next, note, err := updateOpencodeJSONC([]byte(old), "/new/deja", false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(next), `"enabled":false`) || strings.Contains(string(next), `"disabled":true`) {
			t.Errorf("%s\n  was switched off:\n%s", entry, next)
		}
		if strings.Contains(note, "switched off") {
			t.Errorf("%s\n  was reported off: %q", entry, note)
		}
	}
}

// deja's own switched-off entry is not a stranger's on the next install: the
// note compares against what deja writes, switch and all.
func TestASwitchedOffEntryOfDejasIsNotReportedReplacedEveryRun(t *testing.T) {
	old := []byte("{\n  \"mcp\": {\n    \"deja\": {\"type\":\"local\",\"command\":[\"/bin/deja\",\"mcp\"],\"enabled\":false}\n  }\n}\n")
	next, note, err := updateOpencodeJSONC(old, "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "replaced") {
		t.Errorf("deja's own entry was reported as somebody else's: %q", note)
	}
	if !strings.Contains(note, "switched off") {
		t.Errorf("the entry is still off and nothing says so: %q", note)
	}
	if string(next) != string(old) {
		t.Errorf("a repeat install rewrote the entry:\n%s", next)
	}
}
