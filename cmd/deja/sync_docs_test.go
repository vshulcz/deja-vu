package main

import (
	"os"
	"strings"
	"testing"
)

// The Sync format section described the watermark as it was before #982 — per
// source path — while it has been per peer and source since, and folded by
// peers.Identity since #1878. The rules that decide whether two exports are for
// the same machine were written down in Go comments and nowhere a reader would
// look (#1915).
//
// A prose check rather than a key check: this section has no keys, and what it
// has to get right is which of the two names a watermark is filed under.
func TestTheSyncSectionDescribesTheWatermarkItHas(t *testing.T) {
	doc, err := os.ReadFile("../../docs/ARCHITECTURE.md")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(doc), "## Sync format")
	if i < 0 {
		t.Fatal("docs/ARCHITECTURE.md no longer has a Sync format section")
	}
	section := string(doc)[i:]
	if end := strings.Index(section[3:], "\n## "); end > 0 {
		section = section[:end+3]
	}
	prose := strings.Join(strings.Fields(section), " ")

	for _, want := range []struct{ what, phrase string }{
		{"the watermark is per peer as well as per source", "per peer"},
		{"the peers file is named", "peers.json"},
		{"one machine, whatever case its name is typed in", "case"},
		{"a pull learns what the machine calls itself", "learns"},
	} {
		if !strings.Contains(prose, want.phrase) {
			t.Errorf("the Sync format section does not say %s (looked for %q)", want.what, want.phrase)
		}
	}
	if strings.Contains(prose, "The export watermark is per source path (falling back") {
		t.Error("the section still describes the pre-#982 watermark")
	}
}
