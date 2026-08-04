package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Everything deja files under its own harness counted as a "promoted note", so
// forgetting a day of `remember` notes named something the reader had never
// done (#957).
func TestForgetNamesTheKindOfNotesItIsDropping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result index.ForgetResult
		want   string
		avoid  string
	}{
		{"only day buckets", index.ForgetResult{Sessions: 1, Notes: 1}, "a day of your own notes", "promoted"},
		{"several day buckets", index.ForgetResult{Sessions: 2, Notes: 2}, "2 days of your own notes", "promoted"},
		{"only promoted", index.ForgetResult{Sessions: 1, Notes: 1, Promoted: 1}, "promoted note", "day of your own"},
		{"both kinds", index.ForgetResult{Sessions: 4, Notes: 3, Promoted: 1}, "1 promoted, 2 days of notes", ""},
		{"no notes at all", index.ForgetResult{Sessions: 2}, "", "note"},
	} {
		got := forgetNotesLine(tc.result)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%s: said %q about sessions that are not notes", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q does not say %q", tc.name, got, tc.want)
		}
		if tc.avoid != "" && strings.Contains(got, tc.avoid) {
			t.Errorf("%s: %q claims %q", tc.name, got, tc.avoid)
		}
		// No doubled articles from composing the sentence.
		if strings.Contains(got, "a a ") || strings.Contains(got, "is 1 ") {
			t.Errorf("%s: reads badly: %q", tc.name, got)
		}
	}
}
