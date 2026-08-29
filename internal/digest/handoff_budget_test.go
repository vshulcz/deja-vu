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
	add("user", "good — leave the retries alone for now", 401)

	block := Handoff(s, 4000)
	if !strings.Contains(block, "cut the p99 on the orders endpoint") {
		t.Errorf("the problem statement is missing:\n%s", block)
	}
	if !strings.Contains(block, "Key assistant conclusions") {
		t.Errorf("a long session handed over no conclusions section at all:\n%.400s", block)
	}
	if !strings.Contains(block, "per-request pool checkout") {
		t.Errorf("what the session concluded did not survive the budget:\n%.400s", block)
	}
	if len(block) > 4000+200 {
		t.Errorf("the block ran past its budget: %d bytes", len(block))
	}
}
