package nfcfold

import "testing"

// hasCombining scans one block, U+0300–U+036F, and Compose can only act on a
// mark the table knows. The two have to describe the same set: a table entry
// keyed on a mark outside that block would never be reached, because the scan
// returns early and hands the string back untouched.
//
// Today every entry is inside it, which is what makes the narrow scan correct
// rather than lucky. This says so, so that adding U+0483 or a mark from the
// extended blocks fails here instead of quietly doing nothing (#1835).
func TestEveryMarkTheTableUsesIsOneTheScanLooksFor(t *testing.T) {
	seen := 0
	for pair := range compose {
		if mark := pair[1]; !hasCombining(string(mark)) {
			t.Errorf("the table composes %U with %U, which hasCombining does not look for", pair[0], mark)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("the composition table is empty, so this proves nothing")
	}
	// And the scan is not vacuous the other way: ordinary text has no mark.
	if hasCombining("laptop") || hasCombining("数据分片") {
		t.Error("hasCombining reports a mark in text that has none")
	}
}
