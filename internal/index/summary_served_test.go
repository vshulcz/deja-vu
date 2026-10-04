package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A compaction summary names every topic the session touched. Served to an
// ordinary question it carried a marathon into answers about things the session
// never discussed: on 453 real recall questions, 231 first-hit quotes came from
// summaries alone. Asked for by name it is still there (#3384).
func TestTheCompactionSummaryIsServedOnlyWhenAskedFor(t *testing.T) {
	if recordServable(roleSummary, query.Options{}) {
		t.Error("an ordinary query is served a compaction summary")
	}
	if !recordServable(roleSummary, query.Options{Role: roleSummary}) {
		t.Error("--role summary stopped serving summaries")
	}
	if !recordServable("user", query.Options{}) {
		t.Error("an ordinary query lost the person's words")
	}
}
