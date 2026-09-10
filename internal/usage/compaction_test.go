package usage

import (
	"testing"
	"time"
)

func compactionKey(id string) CompactionKey {
	return CompactionKey{Session: id, Workspace: "workspace-a", Revision: "state-v1"}
}

func compactionEvent(at time.Time, kind string, key CompactionKey, baseline, actions int, measured bool, reason string) Event {
	return Event{
		Time:                at,
		Kind:                kind,
		CompactionSession:   key.Session,
		CompactionWorkspace: key.Workspace,
		CompactionRevision:  key.Revision,
		ToolCalls:           baseline,
		ActionsBeforeEdit:   actions,
		Measured:            measured,
		CompactionError:     reason,
	}
}

func TestCompactionRecoverySummarizesOnlyMeasuredFirstEdits(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()
	// Deliberately write a recovery whose wall clock is earlier: reducer order,
	// not a clock that moved backwards, owns the interval pairing.
	for _, e := range []Event{
		compactionEvent(base, KindCompactionCapture, compactionKey("a"), 10, 0, true, ""),
		compactionEvent(base.Add(-time.Minute), KindCompactionRecovery, compactionKey("a"), 0, 4, true, ""),
		compactionEvent(base.Add(time.Minute), KindCompactionCapture, compactionKey("b"), 2, 0, true, ""),
		compactionEvent(base.Add(2*time.Minute), KindCompactionRecovery, compactionKey("b"), 0, 0, true, ""),
		compactionEvent(base.Add(3*time.Minute), KindCompactionCapture, compactionKey("c"), 3, 0, true, ""),
		compactionEvent(base.Add(4*time.Minute), KindCompactionRecovery, compactionKey("c"), 0, 8, true, ""),
		compactionEvent(base.Add(5*time.Minute), KindCompactionCapture, compactionKey("d"), 5, 0, true, ""),
	} {
		appendEventForTest(t, dir, e)
	}

	got := CompactionRecoverySummary(dir)
	if got == nil {
		t.Fatal("missing compaction summary")
	}
	if got.Captures != 4 || got.Measured != 3 || got.Pending != 1 || got.Unmeasured != 0 {
		t.Fatalf("summary = %+v, want captures=4 measured=3 pending=1 unmeasured=0", *got)
	}
	if got.MedianActions != 4 || got.P75Actions != 8 {
		t.Errorf("quantiles = median %g p75 %d, want 4/8", got.MedianActions, got.P75Actions)
	}
	// The existing memory counters must not treat measurement rows as recalls
	// or injections.
	if totals := Totals(dir); totals.Recalls != 0 || totals.Injections != 0 || totals.Bytes != 0 || !totals.Since.IsZero() {
		t.Errorf("measurement rows changed memory totals: %+v", totals)
	}
	if impact := Impact(dir); impact.Recalls != 0 || impact.Injections != 0 || !impact.Since.IsZero() {
		t.Errorf("measurement rows changed impact: %+v", impact)
	}
}

func TestCompactionRecoveryDeduplicatesAndResetsIntervals(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()
	k := compactionKey("same")
	// The second capture is an exactly-identical hook retry. The next distinct
	// capture represents a compaction before any edit and supersedes the old
	// baseline; only the later first edit contributes.
	appendEventForTest(t, dir, compactionEvent(base, KindCompactionCapture, k, 10, 0, true, ""))
	appendEventForTest(t, dir, compactionEvent(base.Add(time.Second), KindCompactionCapture, k, 10, 0, true, ""))
	appendEventForTest(t, dir, compactionEvent(base.Add(2*time.Second), KindCompactionCapture, k, 15, 0, true, ""))
	appendEventForTest(t, dir, compactionEvent(base.Add(3*time.Second), KindCompactionRecovery, k, 0, 2, true, ""))
	// A duplicate first-edit hook cannot make a second sample after the state
	// closed.
	appendEventForTest(t, dir, compactionEvent(base.Add(4*time.Second), KindCompactionRecovery, k, 0, 99, true, ""))

	got := CompactionRecoverySummary(dir)
	if got.Captures != 1 || got.Measured != 1 || got.Pending != 0 || got.Unmeasured != 0 || got.MedianActions != 2 {
		t.Fatalf("summary = %+v, want one 2-action interval", *got)
	}
}

func TestCompactionRecoverySupersedesEarlierRevisionBeforeAnEdit(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()
	first := compactionKey("same-session")
	second := first
	second.Revision = "state-v2"
	appendEventForTest(t, dir, compactionEvent(base, KindCompactionCapture, first, 10, 0, true, ""))
	// A second compaction before any edit is a new persisted revision of the
	// same interval. It replaces the old baseline rather than leaving it as a
	// permanently pending sample.
	appendEventForTest(t, dir, compactionEvent(base.Add(time.Second), KindCompactionCapture, second, 20, 0, true, ""))
	// A delayed old hook cannot close the newer capture.
	appendEventForTest(t, dir, compactionEvent(base.Add(2*time.Second), KindCompactionRecovery, first, 0, 9, true, ""))
	appendEventForTest(t, dir, compactionEvent(base.Add(3*time.Second), KindCompactionRecovery, second, 0, 2, true, ""))

	got := CompactionRecoverySummary(dir)
	if got.Captures != 1 || got.Measured != 1 || got.Pending != 0 || got.Unmeasured != 0 || got.MedianActions != 2 {
		t.Fatalf("summary = %+v, want only the later revision's 2-action interval", *got)
	}
}

func TestCompactionRecoveryCountsUnsupportedAndIncompleteIntervalsAsUnmeasured(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()
	bad := compactionKey("unsupported")
	good := compactionKey("delta-failed")
	appendEventForTest(t, dir, compactionEvent(base, KindCompactionCapture, bad, 0, 0, false, "unsupported"))
	appendEventForTest(t, dir, compactionEvent(base.Add(time.Second), KindCompactionCapture, good, 4, 0, true, ""))
	appendEventForTest(t, dir, compactionEvent(base.Add(2*time.Second), KindCompactionRecovery, good, 0, 0, false, "truncated"))
	// A recovery with no capture could belong to an event rotated away; it must
	// not fabricate a zero-action sample.
	appendEventForTest(t, dir, compactionEvent(base.Add(3*time.Second), KindCompactionRecovery, compactionKey("orphan"), 0, 0, true, ""))
	// Missing workspace cannot be joined to another session at all, so it is
	// one honest unmeasured capture.
	appendEventForTest(t, dir, Event{Time: base.Add(4 * time.Second), Kind: KindCompactionCapture, CompactionSession: "missing", Measured: false, CompactionError: "missing_identity"})

	got := CompactionRecoverySummary(dir)
	if got.Captures != 3 || got.Measured != 0 || got.Pending != 0 || got.Unmeasured != 3 {
		t.Fatalf("summary = %+v, want three unmeasured captures", *got)
	}
	if got.MedianActions != 0 || got.P75Actions != 0 {
		t.Errorf("unmeasured intervals grew a metric: %+v", *got)
	}
}

func TestCompactionRecoveryDeduplicatesFailedCaptureWithoutARevision(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()
	k := CompactionKey{Session: "s", Workspace: "workspace-a"}
	// Storage can fail before it assigns a revision. The same hook firing
	// twice must remain one unmeasured interval, not two failed captures.
	appendEventForTest(t, dir, compactionEvent(base, KindCompactionCapture, k, 0, 0, false, "storage_unavailable"))
	appendEventForTest(t, dir, compactionEvent(base.Add(time.Second), KindCompactionCapture, k, 0, 0, false, "storage_unavailable"))
	got := CompactionRecoverySummary(dir)
	if got.Captures != 1 || got.Measured != 0 || got.Pending != 0 || got.Unmeasured != 1 {
		t.Fatalf("summary = %+v, want one deduplicated unmeasured capture", *got)
	}
}

func TestPendingCompactionSurvivesRestartAndRecoveryClosesIt(t *testing.T) {
	dir := t.TempDir()
	k := compactionKey("restart")
	RecordCompactionCapture(dir, CompactionCapture{Key: k, ToolCalls: 11})
	got, ok := PendingCompaction(dir, k)
	if !ok || got.ToolCalls != 11 {
		t.Fatalf("pending = %+v/%v, want baseline 11", got, ok)
	}
	RecordCompactionRecovery(dir, CompactionRecovery{Key: k, ActionsBeforeEdit: 3})
	if _, ok := PendingCompaction(dir, k); ok {
		t.Fatal("recovery left compaction pending")
	}

	// Capture errors and invalid identities cannot be mistaken for a baseline.
	RecordCompactionCapture(dir, CompactionCapture{Key: compactionKey("unsupported"), Error: "unsupported"})
	if _, ok := PendingCompaction(dir, compactionKey("unsupported")); ok {
		t.Fatal("unsupported capture became pending")
	}
	if _, ok := PendingCompaction(dir, CompactionKey{Session: "only-session"}); ok {
		t.Fatal("incomplete key became pending")
	}
}

func TestPendingCompactionDoesNotReturnASupersededRevision(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()
	first := compactionKey("pending")
	failed := first
	failed.Revision = ""
	appendEventForTest(t, dir, compactionEvent(base, KindCompactionCapture, first, 5, 0, true, ""))
	appendEventForTest(t, dir, compactionEvent(base.Add(time.Second), KindCompactionCapture, failed, 0, 0, false, "storage_unavailable"))
	if _, ok := PendingCompaction(dir, first); ok {
		t.Fatal("a failed newer capture left the old revision pending")
	}

	second := first
	second.Revision = "state-v2"
	appendEventForTest(t, dir, compactionEvent(base.Add(2*time.Second), KindCompactionCapture, second, 9, 0, true, ""))
	// A delayed recovery for the superseded state must not close the current
	// pending capture.
	appendEventForTest(t, dir, compactionEvent(base.Add(3*time.Second), KindCompactionRecovery, first, 0, 1, true, ""))
	got, ok := PendingCompaction(dir, second)
	if !ok || got.ToolCalls != 9 {
		t.Fatalf("pending = %+v/%v, want later baseline 9", got, ok)
	}
}

func TestCompactionRecordersMarkInvalidInputUnmeasured(t *testing.T) {
	dir := t.TempDir()
	complete := compactionKey("invalid")
	RecordCompactionCapture(dir, CompactionCapture{Key: complete, ToolCalls: -1})
	RecordCompactionCapture(dir, CompactionCapture{Key: CompactionKey{Session: "missing", Workspace: "workspace-a"}})
	RecordCompactionRecovery(dir, CompactionRecovery{Key: complete, ActionsBeforeEdit: -1})
	RecordCompactionRecovery(dir, CompactionRecovery{Key: CompactionKey{Session: "missing", Workspace: "workspace-a"}})

	events := Events(dir, 0)
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	// Events returns newest-first; every invalid input must retain its reason
	// instead of looking like a measured zero.
	got := map[string]bool{}
	for _, e := range events {
		if e.Measured {
			t.Errorf("invalid input recorded as measured: %+v", e)
		}
		got[e.CompactionError] = true
	}
	for _, want := range []string{"invalid_baseline", "missing_identity", "invalid_action_count"} {
		if !got[want] {
			t.Errorf("missing reason %q in %v", want, got)
		}
	}
}
