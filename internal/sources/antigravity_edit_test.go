package sources

import (
	"strings"
	"testing"
	"time"
)

// An edit through replace_file_content names its file in a sentence, not on
// a "File Path:" line, and carries the change as a diff block; the step gave
// only tool output, so blame and files never saw the edit (#3279). Shape from
// a real IDE transcript.
func TestAntigravityEditStepNamesTheFileAndTheChange(t *testing.T) {
	content := "Created At: 2026-06-29T12:03:31Z\nCompleted At: 2026-06-29T12:03:37Z\n" +
		"The following changes were made by the replace_file_content tool to: /Users/me/coding/app/internal/refund/application.go. If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.\n" +
		"[diff_block_start]\n@@ -18,14 +18,17 @@\n \tRunInTx(ctx context.Context) error\n }\n \n-// Application handles order refund operations\n+// OrderService defines the interface for order operations\n+type OrderService interface {\n+}\n[diff_block_end]\n"
	at := time.Date(2026, 6, 29, 12, 3, 31, 0, time.UTC)
	var files, edits []string
	for _, m := range antigravityStep("CODE_ACTION", content, at) {
		switch m.Role {
		case RoleFiles:
			files = append(files, m.Text)
		case RoleEdit:
			edits = append(edits, m.Text)
		}
	}
	if len(files) != 1 || files[0] != "/Users/me/coding/app/internal/refund/application.go" {
		t.Errorf("files = %q, want the edited path", files)
	}
	if len(edits) != 1 || !strings.HasPrefix(edits[0], "/Users/me/coding/app/internal/refund/application.go\n") ||
		!strings.Contains(edits[0], "// Application handles order refund operations") {
		t.Errorf("edits = %q, want the path and the removed lines", edits)
	}
	// write_to_file says the same sentence with its own tool name.
	created := "Created At: 2026-06-29T12:05:00Z\nThe following changes were made by the write_to_file tool to: /Users/me/coding/app/new.go. If relevant, run it.\n"
	var got []string
	for _, m := range antigravityStep("CODE_ACTION", created, at) {
		if m.Role == RoleFiles {
			got = append(got, m.Text)
		}
	}
	if len(got) != 1 || got[0] != "/Users/me/coding/app/new.go" {
		t.Errorf("write_to_file files = %q", got)
	}
}
