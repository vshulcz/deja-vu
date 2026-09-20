package sources

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// The same diff block carries the written side, and some blocks on a real
// store carry unified headers — a "+++ b/<path>" line is long enough to be
// hashed as a written line, and would then answer blame with a file header
// nobody wrote (#3773).
func TestAntigravityRecordsTheWrittenSideOfADiffBlock(t *testing.T) {
	const added = "// OrderService defines the interface for order operations"
	const removed = "// Application handles order refund operations"
	const header = "+++ b/internal/refund/application.go"
	content := "Created At: 2026-06-29T12:03:31Z\n" +
		"The following changes were made by the replace_file_content tool to: /Users/me/coding/app/internal/refund/application.go. If relevant, run it.\n" +
		"[diff_block_start]\n--- a/internal/refund/application.go\n" + header + "\n@@ -18,14 +18,17 @@\n" +
		" \tRunInTx(ctx context.Context) error\n-" + removed + "\n+" + added +
		"\n+type OrderService interface {\n[diff_block_end]\n"
	at := time.Date(2026, 6, 29, 12, 3, 31, 0, time.UTC)

	var wrote []string
	for _, m := range antigravityStep("CODE_ACTION", content, at) {
		if m.Role == RoleWrote {
			wrote = append(wrote, m.Text)
		}
	}
	if len(wrote) != 1 {
		t.Fatalf("wrote = %q, want one record", wrote)
	}
	h, ok := HashWrittenLine(added)
	if !ok {
		t.Fatal("the added line is not evidence")
	}
	path, has := WroteRecordHas(wrote[0], h)
	if !has {
		t.Errorf("the record does not hold the added line: %q", wrote[0])
	}
	if path != "/Users/me/coding/app/internal/refund/application.go" {
		t.Errorf("the record is filed under %q", path)
	}
	// Nothing else in the block is a written line: not the removed side, not
	// the context line, and not the headers in either of the forms a missing
	// guard would leave them ("+++ b/x" or "++ b/x").
	want := map[string]bool{}
	for _, line := range []string{added, "type OrderService interface {"} {
		if h, ok := HashWrittenLine(line); ok {
			want[strconv.FormatUint(h, 16)] = true
		}
	}
	_, hashes, _ := strings.Cut(wrote[0], "\n")
	for _, got := range strings.Fields(hashes) {
		if !want[got] {
			t.Errorf("the record holds a hash of something the block did not write: %q", wrote[0])
		}
	}
	if len(strings.Fields(hashes)) != len(want) {
		t.Errorf("hashes = %d, want %d", len(strings.Fields(hashes)), len(want))
	}
}
