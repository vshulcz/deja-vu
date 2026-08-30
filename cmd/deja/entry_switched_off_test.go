package main

import (
	"strings"
	"testing"
)

// An entry the reader switched off stays off, which is right — and the note
// says so, so nobody wires deja and then wonders why nothing recalls. That note
// was keyed to `disabled`, and opencode, grok and openclaw all write `enabled`,
// so on those files install reported success and said nothing (#2757).
func TestMergeNamesAnEntryLeftSwitchedOff(t *testing.T) {
	next := map[string]any{"type": "local", "command": []string{"/bin/deja", "mcp"}}

	for _, off := range []map[string]any{
		{"command": []any{"/old/deja", "mcp"}, "disabled": true},
		{"command": []any{"/old/deja", "mcp"}, "enabled": false},
	} {
		merged, note := mergeDejaEntry(off, next)
		if !strings.Contains(note, "turn it back on") {
			t.Errorf("nothing said the entry is still off for %v: %q", off, note)
		}
		if _, ok := merged["disabled"]; ok {
			if merged["disabled"] != true {
				t.Errorf("switched the entry back on: %v", merged)
			}
		}
		if v, ok := merged["enabled"]; ok && v != false {
			t.Errorf("switched the entry back on: %v", merged)
		}
	}

	// An entry that is on says nothing of the sort.
	_, note := mergeDejaEntry(map[string]any{"command": []any{"/old/deja", "mcp"}, "enabled": true}, next)
	if strings.Contains(note, "turn it back on") {
		t.Errorf("called a live entry switched off: %q", note)
	}
}
