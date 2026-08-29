package digest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// "A marker in the middle of a block is worse than none: it says the passage it
// follows was cut and lets the next one read as continuous. Once a chunk is
// cut, the block ends." — TestNothingFollowsAMarkedCut, which holds that for
// `Share` and for the tail on their own. The handoff composes both and put a
// whole section, four messages and a closing paragraph after the marker (#2464).
func TestAHandoffDoesNotContinuePastACutMarker(t *testing.T) {
	now := time.Now()
	s := model.Session{ID: "long", Harness: "claude", Project: "work/app", Updated: now}
	add := func(role, text string, i int) {
		s.Messages = append(s.Messages, model.Message{
			Role: role, Text: text, Time: now.Add(-time.Duration(600-i) * time.Minute),
		})
	}
	add("user", "we need to cut the p99 on the orders endpoint from 900ms to under 300ms", 0)
	for i := 0; i < 96; i++ {
		add("assistant", fmt.Sprintf("step %d: reading handler_%d.go and measuring the %dth span, weighing whether the pool or the retries explain it", i, i, i), 1+2*i)
		add("user", fmt.Sprintf("go on (%d)", i), 2+2*i)
	}
	add("assistant", "the cause was the per-request pool checkout; a shared pool and a 200ms budget brought p99 to 240ms", 400)
	add("user", "good — leave the retries alone for now", 401)
	add("assistant", "stopping here: the pool change is merged, the retry work is untouched", 402)

	for budget := 2000; budget <= 8000; budget += 500 {
		block := Handoff(s, budget)
		i := strings.Index(block, "…")
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(block[i+len("…"):])
		// The closing sentence is deja's own framing and always ends the block;
		// what must not follow the marker is more of the session.
		rest = strings.TrimSpace(strings.TrimPrefix(rest, closingLine(s)))
		if rest != "" {
			t.Fatalf("budget %d: the handoff continues past the marker with %.200q", budget, rest)
		}
	}
}

// closingLine is the sentence Handoff ends with, for the test above.
func closingLine(s model.Session) string {
	short := idSelector(Short(s.ID))
	return fmt.Sprintf("This is a compact slice of session %s. If anything you need is missing — an exact error, a file, a decision — search the full history with `deja \"<term>\"` or `deja show %s`, or call the deja MCP tools recall / recall_context if available.", short, short)
}
