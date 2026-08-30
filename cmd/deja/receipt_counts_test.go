package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// #1569 settled the daily counters on what an agent asked for and got, with
// injections reported separately. The receipt — the one string that rides into
// the model's context — was not part of that, so on a machine that lives on
// auto-recall the statusline said "no agent recalls today" while the receipt
// called the same five injections recalls (#1575).
func TestTheReceiptCallsInjectionsWhatTheStatuslineCallsThem(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		usage.RecordDigest(dir, usage.KindHook, strings.Repeat("x", 400), 2, 4000)
	}

	got := serviceReceipt(dir)
	if strings.Contains(got, "recall") {
		t.Errorf("injections are counted as recalls again: %q", got)
	}
	if !strings.Contains(got, "5") {
		t.Errorf("the day the reader had is not in the receipt: %q", got)
	}
	n := usage.StatusCounters(dir)
	if n.Recalls != 0 {
		t.Fatalf("the premise moved: the statusline counts %d recalls for a hook-only day", n.Recalls)
	}
}

// A day an agent asked for memory itself still reads as recalls.
func TestTheReceiptStillCountsWhatAnAgentAskedFor(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		usage.RecordDigest(dir, usage.KindRecall, strings.Repeat("x", 400), 2, 4000)
	}

	got := serviceReceipt(dir)
	if !strings.Contains(got, "3 recalls") {
		t.Errorf("what the agent asked for is no longer counted: %q", got)
	}
}

// And a day with both says both, rather than folding one into the other.
func TestTheReceiptSeparatesWhatWasAskedForFromWhatArrived(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		usage.RecordDigest(dir, usage.KindRecall, strings.Repeat("x", 400), 2, 4000)
	}
	for i := 0; i < 2; i++ {
		usage.RecordDigest(dir, usage.KindHook, strings.Repeat("x", 400), 2, 4000)
	}

	got := serviceReceipt(dir)
	if !strings.Contains(got, "3 recalls") {
		t.Errorf("the recalls are gone: %q", got)
	}
	if !strings.Contains(got, "2") {
		t.Errorf("the injections are folded into the recalls: %q", got)
	}
}
