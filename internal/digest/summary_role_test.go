package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The parsers now file Claude's compaction summary under the summary role. The
// packet's objective and a resumed session's earlier half still read it there,
// as they read it under the user role from an index built before.
func TestSummaryRoleStillCarriesTheCompactedIntent(t *testing.T) {
	summary := "This session is being continued from a previous conversation that ran out of context.\n\nSummary:\n1. Primary Request and Intent:\n  Repair the decoder and add a fixture.\n2. Key Technical Concepts:\n  - irrelevant\n"
	for _, role := range []string{sources.RoleSummary, "user"} {
		s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
			{Role: role, Text: summary},
			{Role: "user", Text: "go on"},
		}}
		c := ExtractCompactionContext(s, ExtractOptions{})
		if !strings.Contains(c.Objective.Text, "Repair the decoder") {
			t.Errorf("role %q: summary intent was not retained: %#v", role, c.Objective)
		}
		if got := compactedHalf(s); !strings.Contains(got, "Repair the decoder") {
			t.Errorf("role %q: compactedHalf = %q", role, got)
		}
	}
	// A session that opens with the person's own words has no compacted half.
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "fix the decoder"},
		{Role: sources.RoleSummary, Text: summary},
	}}
	if got := compactedHalf(s); got != "" {
		t.Errorf("compactedHalf read a summary that came after the person spoke: %q", got)
	}
}
