package digest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A handoff is meant to carry the problem, what was concluded, and where it
// stopped. Sections are written in order until the budget runs out, so a long
// session — where the user side is mostly "go on" — spent the whole block on
// problem statements and the conclusions never got a line, which is the half
// the receiving agent cannot re-derive (#2462).
func TestALongSessionStillHandsOverItsConclusions(t *testing.T) {
	now := time.Now()
	s := model.Session{ID: "long", Harness: "claude", Project: "work/app", Updated: now}
	add := func(role, text string, i int) {
		s.Messages = append(s.Messages, model.Message{
			Role: role, Text: text, Time: now.Add(-time.Duration(600-i) * time.Minute),
		})
	}
	add("user", "we need to cut the p99 on the orders endpoint from 900ms to under 300ms", 0)
	for i := 0; i < 96; i++ {
		add("assistant", fmt.Sprintf("step %d: reading handler_%d.go and measuring that span; nothing conclusive yet", i, i), 1+2*i)
		add("user", fmt.Sprintf("go on (%d)", i), 2+2*i)
	}
	add("assistant", "the cause was the per-request pool checkout; a shared pool and a 200ms budget brought p99 to 240ms", 400)
	// The last exchange is long, so the tail is something the budget has to
	// cut rather than something that fits whatever it is given: that is the
	// half the packaging can overshoot on (#2866).
	add("user", "good — leave the retries alone for now, and write up what we changed: "+
		strings.Repeat("the pool checkout moved out of the request path; ", 40), 401)

	block := Handoff(s, 4000, nil)
	if !strings.Contains(block, "cut the p99 on the orders endpoint") {
		t.Errorf("the problem statement is missing:\n%s", block)
	}
	if !strings.Contains(block, "Key assistant conclusions") {
		t.Errorf("a long session handed over no conclusions section at all:\n%.400s", block)
	}
	if !strings.Contains(block, "per-request pool checkout") {
		t.Errorf("what the session concluded did not survive the budget:\n%.400s", block)
	}
	// The slack is what the package writes outside the budget: the opening
	// sentence and the closing "deja show <id>" pointer.
	if len(block) > 4000+300 {
		t.Errorf("the block ran past its budget: %d bytes", len(block))
	}
	// The quoted half is written into a builder of its own now, and the tail's
	// budget has to count it: measured, it was handed the whole body's worth
	// of extra room and a 2000-byte package came back at 3159 (#2866).
	const open, close = "<q>\n", "\n</q>"
	// A tighter budget than the session, so the tail is what the packaging has
	// to cut — at 4000 this fixture never fills it and the bound proves
	// nothing.
	for _, budget := range []int{2000, 4000} {
		framed := Handoff(s, budget, func(text string) string { return open + text + close })
		// The slack is what the package writes outside the budget: the
		// opening sentence and the closing "deja show <id>" pointer, ~280
		// bytes, which is the same on base.
		if len(framed) > budget+300+len(open)+len(close) {
			t.Errorf("the framed block ran past its %d budget: %d bytes", budget, len(framed))
		}
	}
}
