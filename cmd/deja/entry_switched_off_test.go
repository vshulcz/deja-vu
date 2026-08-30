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
