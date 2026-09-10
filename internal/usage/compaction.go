package usage

import (
	"sort"
	"strings"
	"time"
)

// CompactionKey identifies one host session at one workspace and schema
// revision. The revision prevents a capture made against an older persisted
// state from being paired with an edit after that state changed.
type CompactionKey struct {
	Session   string
	Workspace string
	Revision  string
}

func (k CompactionKey) valid() bool {
	return strings.TrimSpace(k.Session) != "" &&
		strings.TrimSpace(k.Workspace) != "" &&
		strings.TrimSpace(k.Revision) != ""
}

// identifiable is deliberately weaker than valid. A failed precompact capture
// can know its host session and workspace before it can create an index-state
// revision. Keeping that incomplete identity lets the summary collapse a
// duplicate failing hook into one unmeasured interval, while PendingCompaction
// still requires a complete identity before it can hand a baseline to a later
// edit.
func (k CompactionKey) identifiable() bool {
	return strings.TrimSpace(k.Session) != "" && strings.TrimSpace(k.Workspace) != ""
}

func (k CompactionKey) eventFields(e *Event) {
	e.CompactionSession = k.Session
	e.CompactionWorkspace = k.Workspace
	e.CompactionRevision = k.Revision
}

func keyFromEvent(e Event) CompactionKey {
	return CompactionKey{
		Session:   e.CompactionSession,
		Workspace: e.CompactionWorkspace,
		Revision:  e.CompactionRevision,
	}
}

// CompactionCapture is the baseline read from the raw transcript before a
// host compacts it. Error is a short machine-readable reason such as
// "unsupported" or "truncated"; it must not contain transcript content.
type CompactionCapture struct {
	Key       CompactionKey
	ToolCalls int
	Error     string
}

// CompactionRecovery is the outcome at the first edit after a capture.
// ActionsBeforeEdit excludes that edit itself. Error has the same restricted
// meaning as CompactionCapture.Error.
type CompactionRecovery struct {
	Key               CompactionKey
	ActionsBeforeEdit int
	Error             string
}

// RecordCompactionCapture records a compaction baseline in the existing usage
// sidecar. Recording is best effort, like every other usage event. A failed or
// incomplete capture is retained as unmeasured so stats never present it as a
// zero-action recovery.
func RecordCompactionCapture(indexDir string, capture CompactionCapture) {
	errText := strings.TrimSpace(capture.Error)
	if !capture.Key.valid() && errText == "" {
		errText = "missing_identity"
	}
	if capture.ToolCalls < 0 && errText == "" {
		errText = "invalid_baseline"
	}
	e := Event{
		Time:            time.Now().UTC(),
		Kind:            KindCompactionCapture,
		ToolCalls:       capture.ToolCalls,
		Measured:        errText == "",
		CompactionError: errText,
	}
	capture.Key.eventFields(&e)
	recordEvent(indexDir, e)
}

// RecordCompactionRecovery records the first-edit outcome for a prior
// compaction capture. Callers should use PendingCompaction to find that
// capture before calculating the raw transcript delta.
func RecordCompactionRecovery(indexDir string, recovery CompactionRecovery) {
	errText := strings.TrimSpace(recovery.Error)
	if !recovery.Key.valid() && errText == "" {
		errText = "missing_identity"
	}
	if recovery.ActionsBeforeEdit < 0 && errText == "" {
		errText = "invalid_action_count"
	}
	e := Event{
		Time:              time.Now().UTC(),
		Kind:              KindCompactionRecovery,
		ActionsBeforeEdit: recovery.ActionsBeforeEdit,
		Measured:          errText == "",
		CompactionError:   errText,
	}
	recovery.Key.eventFields(&e)
	recordEvent(indexDir, e)
}

// PendingCompaction returns the latest still-open, measurable capture for
// key. It reconstructs state from the usage log so a host restart cannot lose
// an interval. A later capture resets an earlier one, and a recovery closes it.
func PendingCompaction(indexDir string, key CompactionKey) (CompactionCapture, bool) {
	if !key.valid() {
		return CompactionCapture{}, false
	}
	var pending CompactionCapture
	ok := false
	active := false
	activeRevision := ""
	interval := intervalKey(key)
	for _, e := range read(Path(indexDir)) {
		eventKey := keyFromEvent(e)
		if !eventKey.identifiable() || intervalKey(eventKey) != interval {
			continue
		}
		switch e.Kind {
		case KindCompactionCapture:
			// A newer capture supersedes any prior revision of this session and
			// workspace, including a failed capture without a revision. Do not
			// hand an edit a baseline that belongs to the discarded packet.
			active = true
			activeRevision = eventKey.Revision
			if eventKey == key && e.Measured && e.CompactionError == "" && e.ToolCalls >= 0 {
				pending = CompactionCapture{Key: eventKey, ToolCalls: e.ToolCalls}
				ok = true
			} else {
				ok = false
			}
		case KindCompactionRecovery:
			// A late recovery for a superseded revision must not close the
			// current capture. The summary makes the same distinction.
			if active && eventKey.Revision == activeRevision {
				active = false
				ok = false
			}
		}
	}
	return pending, active && activeRevision == key.Revision && ok
}

// CompactionSummary is the local, retained-window measure of how many raw
// transcript tool calls followed compaction before a session first edited.
// MedianActions is allowed to be fractional for an even number of samples.
// The object is absent when no compaction event remains in the usage log.
type CompactionSummary struct {
	Captures      int     `json:"captures"`
	Measured      int     `json:"measured"`
	Pending       int     `json:"pending"`
	Unmeasured    int     `json:"unmeasured"`
	MedianActions float64 `json:"median_actions"`
	P75Actions    int     `json:"p75_actions"`
}

type compactionState struct {
	measurable bool
	revision   string
}

// compactionIntervalKey deliberately leaves revision out. A second
// compaction in the same host session/workspace supersedes the one the agent
// never edited after, even though the durable continuation state receives a
// new revision. The revision remains on compactionState so a late recovery for
// the superseded capture cannot close the newer interval.
type compactionIntervalKey struct {
	session   string
	workspace string
}

func intervalKey(k CompactionKey) compactionIntervalKey {
	return compactionIntervalKey{session: k.Session, workspace: k.Workspace}
}

// CompactionRecoverySummary derives the compaction measurement from usage
// events in file order. File order is intentional: clock skew must not pair a
// recovery with a later capture merely because its timestamp sorts differently.
func CompactionRecoverySummary(indexDir string) *CompactionSummary {
	states := map[compactionIntervalKey]compactionState{}
	samples := []int{}
	var out CompactionSummary
	seen := false

	for _, e := range read(Path(indexDir)) {
		if e.Kind != KindCompactionCapture && e.Kind != KindCompactionRecovery {
			continue
		}
		seen = true
		key := keyFromEvent(e)
		if !key.identifiable() {
			// An identity-less row cannot safely join another session. It is
			// still an honest observation that did not produce a sample.
			if e.Kind == KindCompactionCapture {
				out.Captures++
				out.Unmeasured++
			}
			continue
		}

		interval := intervalKey(key)
		s, active := states[interval]
		switch e.Kind {
		case KindCompactionCapture:
			// A second capture before any first-edit outcome is either a
			// duplicate hook or a newer compaction that supersedes the first.
			// Both deliberately stay one open interval for this session/workspace.
			if active {
				states[interval] = compactionState{
					measurable: e.Measured && e.CompactionError == "" && e.ToolCalls >= 0,
					revision:   key.Revision,
				}
				continue
			}
			out.Captures++
			states[interval] = compactionState{
				measurable: e.Measured && e.CompactionError == "" && e.ToolCalls >= 0,
				revision:   key.Revision,
			}
		case KindCompactionRecovery:
			if !active || s.revision != key.Revision {
				// The capture may have rotated away. Guessing that this is a
				// complete interval — or letting an old recovery close a newer
				// capture — would fabricate a baseline.
				continue
			}
			delete(states, interval)
			if !s.measurable || !e.Measured || e.CompactionError != "" || e.ActionsBeforeEdit < 0 {
				out.Unmeasured++
				continue
			}
			out.Measured++
			samples = append(samples, e.ActionsBeforeEdit)
		}
	}
	if !seen {
		return nil
	}
	for _, s := range states {
		if s.measurable {
			out.Pending++
		} else {
			out.Unmeasured++
		}
	}
	if len(samples) == 0 {
		return &out
	}
	sort.Ints(samples)
	mid := len(samples) / 2
	if len(samples)%2 == 0 {
		out.MedianActions = float64(samples[mid-1]+samples[mid]) / 2
	} else {
		out.MedianActions = float64(samples[mid])
	}
	// Nearest-rank p75: with one sample it is that sample, and with four it is
	// the third, which is the conventional 75th-percentile action count.
	rank := (3*len(samples) + 3) / 4
	out.P75Actions = samples[rank-1]
	return &out
}
