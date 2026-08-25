package termwidth

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/nfcfold"
)

// Every caller measures with Columns and then cuts with Cut, so the two have to
// agree about the same string: `if Columns(s) > max { Cut(s, max) }`.
func TestCutAgreesWithColumns(t *testing.T) {
	for _, s := range []string{
		"über-server-in-the-other-office",
		"über-server-in-the-other-office",
		"naïve-project",
		"数据分片服务器",
		"🚀rocket-launcher",
		"plain-ascii-name",
	} {
		for _, max := range []int{1, 4, 8, 12, 20} {
			cut := Cut(s, max)
			if got := Columns(cut); got > max {
				t.Errorf("Cut(%q, %d) measures %d columns", s, max, got)
			}
			// A string that fits comes back whole. It comes back composed —
			// the cut normalises what it measures, so the two cannot disagree
			// about where a character ends.
			if Columns(s) <= max && cut != nfcfold.Compose(s) {
				t.Errorf("Cut(%q, %d) shortened a string that already fits: %q", s, max, cut)
			}
		}
	}
}
