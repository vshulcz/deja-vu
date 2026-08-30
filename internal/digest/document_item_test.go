package digest

import (
	"fmt"
	"strings"
	"testing"
)

// A specification the agent wrote during the session is not a statement about
// the session. One of its bullets was being recalled to a later agent as a past
// decision.
func TestDocumentItemIsNotWhatTheSessionConcluded(t *testing.T) {
	var b strings.Builder
	b.WriteString("## Goal\n")
	b.WriteString("- Consolidate scattered AI work into one workspace\n")
	b.WriteString("## Constraints & Preferences\n")
	for i := range 30 {
		fmt.Fprintf(&b, "- rule %d: keep responses concise by default\n", i)
	}
	spec := b.String()

	if !IsDocumentItem(spec, "- rule 7: keep responses concise by default") {
		t.Error("a bullet of a thirty-line specification was taken for a conclusion")
	}
	// The prose in the same message still describes the work.
	if IsDocumentItem(spec, "Ниже — черновик правил, это будущий источник правды") {
		t.Error("the prose introducing the document was dropped with it")
	}

	// An ordinary reply that happens to list three things is not a document.
	reply := "Починил. Причина была в трёх местах:\n- gate\n- cache\n- hook\nПроверил на реальном сторе."
	if IsDocumentItem(reply, "- gate") {
		t.Error("a short list inside a normal answer was treated as a document")
	}
	// And a table row in a long table is structure like any other.
	var tb strings.Builder
	for i := range 25 {
		fmt.Fprintf(&tb, "| bench%d | six fixed queries against a real index | median |\n", i)
	}
	if !IsDocumentItem(tb.String(), "| bench3 | six fixed queries against a real index | median |") {
		t.Error("a row of a long table was taken for a conclusion")
	}
}

// A marker with no space after it is not a marker. Found by reading what the
// document rule still skipped: a bold sentence opens with an asterisk and a
// pointer opens with a dash, and counting either as structure lets a real
// conclusion be dropped as a list item.
func TestBoldAndArrowsAreNotStructure(t *testing.T) {
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "- rule %d: something the document says\n", i)
	}
	doc := b.String()

	for _, line := range []string{
		"**1. caffeinate — in a separate terminal** so it survives the session",
		"-> the retry fires only after the second failure",
		"#2740 is the issue this came from",
	} {
		if IsDocumentItem(doc, line) {
			t.Errorf("a sentence was taken for a list item: %q", line)
		}
	}
	for _, line := range []string{
		"- rule 3: something the document says",
		"## Constraints",
		"| bench | queries | median |",
	} {
		if !IsDocumentItem(doc, line) {
			t.Errorf("real structure went unrecognised: %q", line)
		}
	}
}
