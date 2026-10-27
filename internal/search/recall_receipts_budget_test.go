package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/evidence"
)

// The line that answers keeps its room; the records that back it give theirs
// up first, the last session's first.
func TestReceiptsGiveWayToTheAnswerInABlock(t *testing.T) {
	first := []string{
		"- **orchard** `a1` · 2026-01-02\n",
		"  - " + evidence.LabelUser + "why does the planner skip pear rows\n",
		"  - Ran: `go test ./internal/planner -run TestPearRows` → exit 0, output: ok planner 0.4s\n",
		"  - Output: " + strings.Repeat("planner row dump ", 12) + "\n",
		"  - " + evidence.LabelModelSaid + "the pear filter compared seasons by name; switched it to the season id\n",
	}
	second := []string{
		"- **orchard** `b2` · 2026-01-03\n",
		"  - Edit internal/planner/rows.go: replaced `season.Name == want`\n",
		"  - " + evidence.LabelModelSaid + "pear rows come back once the cache key carries the season id\n",
	}
	size := func(secs ...[]string) int {
		n := 0
		for _, s := range secs {
			for _, l := range s {
				n += len(l)
			}
		}
		return n
	}
	total := size(first, second)
	budget := total - 40
	secs := [][]string{first, second}
	dropReceipts(secs, total, budget)
	got := strings.Join(append(append([]string{}, secs[0]...), secs[1]...), "")
	if size(secs...) > budget {
		t.Fatalf("still over budget: %d > %d", size(secs...), budget)
	}
	for _, want := range []string{"switched it to the season id", "cache key carries the season id", "Ran: `go test"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Edit internal/planner") {
		t.Errorf("the last session's receipt stayed while over budget:\n%s", got)
	}
	if !receiptLine("  - Output: x") || receiptLine("  - "+evidence.LabelModelSaid+"Output: x") {
		t.Error("receiptLine misread a line")
	}
}
