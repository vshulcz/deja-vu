// Package usage records when deja serves memory to an agent — MCP recalls
// and session-start hook injections — so `deja statusline` can show activity.
// Recording is best-effort by design: a failure to write a usage event must
// never break the recall path itself.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// Event kinds. Search is tracked too but statusline only counts memory
// served to agents (recall, context, hook).
const (
	KindRecall  = "recall"
	KindContext = "recall_context"
	KindHook    = "hook"
	KindSearch  = "search"
	// KindBlame is the MCP blame tool. It answers the agent like recall does,
	// and it is the largest thing the server hands over — whole sessions
	// rather than budgeted snippets — so leaving it out understated what the
	// agent was given (#682).
	KindBlame = "blame"
	// KindResource is a read of deja://session/… over the MCP resources
	// surface. It hands the agent a whole session in the same frame
	// recall_context uses, so leaving it out understated what was served the
	// same way blame did before #682.
	KindResource = "resource"
	KindHandoff  = "handoff"
	// KindRemember is the MCP remember tool — the one tool that writes to the
	// store. #682 covered the read tools only, so an agent could add a note
	// that showed up nowhere in the journal the user reads.
	KindRemember = "remember"
	// KindDejaVu marks a per-prompt recall: the user asked something their
	// own history already answers — the product's namesake moment.
	KindDejaVu = "dejavu"
	// KindTool is the PreToolUse hook injection — one line about a command or
	// file the agent is acting on. It is deduped per session, so it counts a
	// distinct fact served, not every action. Leaving it out made deja's most
	// frequent injection surface invisible to stats and the receipt.
	KindTool = "tool"
)

// servedKinds are the events that hand an agent memory it asked for: the two
// recall tools, a blame — "the largest thing the server hands over", which is
// why #682 gave it a kind of its own — and a read of deja://session/…, which
// hands over a whole session in the same frame recall_context uses.
//
// Named once because the counters drifted: #1569 aligned five of them and left
// Impact behind, so a blame was a recall on `deja stats` and nothing on
// `deja stats --impact`, whose distilled ratio was then short by its bytes
// (#1907).
func servedKind(kind string) bool {
	switch kind {
	case KindRecall, KindContext, KindBlame, KindResource:
		return true
	}
	return false
}

// injectedKinds are the events where deja offers memory unasked: the
// session-start hook, the per-prompt recall and the PreToolUse line.
func injectedKind(kind string) bool {
	switch kind {
	case KindHook, KindDejaVu, KindTool:
		return true
	}
	return false
}

type Event struct {
	Time     time.Time `json:"t"`
	Kind     string    `json:"kind"`
	Bytes    int       `json:"bytes"`
	Sessions int       `json:"sessions,omitempty"`
	Empty    bool      `json:"empty,omitempty"`
	// RawBytes is the size of the source transcripts the served digest was
	// distilled from — what the agent would have had to replay without deja.
	RawBytes int64 `json:"raw,omitempty"`
	// SessionIDs lists the sessions served by an agent-initiated recall, so
	// search can weigh what agents actually re-used.
	SessionIDs []string `json:"ids,omitempty"`
}

type Summary struct {
	// Recalls counts agent-initiated recalls only. Injections are counted
	// separately and printed on their own line — folding them into Recalls
	// made one surface say 5 and `deja stats --impact` say 2 about the same
	// five events.
	Recalls          int     `json:"recalls_served"`
	Injections       int     `json:"injections"`
	RecallSessions   int     `json:"recall_sessions"`
	InjectedSessions int     `json:"injected_sessions"`
	Bytes            int     `json:"bytes"`
	InjectedBytes    int     `json:"injected_bytes"`
	RawBytes         int64   `json:"raw_bytes,omitempty"`
	DejaVuMoments    int     `json:"dejavu_moments,omitempty"`
	EmptyResultRate  float64 `json:"empty_result_rate"`
	// Since is the oldest event still in the log. The log is rewritten past
	// 1MB keeping the last 14 days, so a count with no period attached reads
	// as a lifetime total and then falls by orders of magnitude when that
	// happens (#763).
	Since time.Time `json:"-"`
}

// MarshalJSON writes Since only when there is one. `omitempty` does nothing to
// a struct, and time.Time is one, so the tag alone wrote January of year 1 on
// every store that had served no recall — a date a reader subtracts from, and
// one the document says is not there at all (#1874).
func (s Summary) MarshalJSON() ([]byte, error) {
	type plain Summary
	out := struct {
		plain
		Since *time.Time `json:"since,omitempty"`
	}{plain: plain(s)}
	if !s.Since.IsZero() {
		out.Since = &s.Since
	}
	return json.Marshal(out)
}

const (
	rotateAt   = 1 << 20 // rewrite the log when it grows past 1MB
	keepWindow = 14 * 24 * time.Hour
)

// ahead reports a timestamp past the end of the reader's day — a clock that
// ran fast before it was corrected, or a log carried over from such a machine.
//
// The counters are windows on the recent past, and an event dated ahead of the
// window sits inside every one of them: measured with a single event stamped a
// year out, "today" reported it every day for a year, and the rotation that
// bounds the log keeps whatever is newer than its cutoff, so it never aged
// out. The end of the day rather than the instant, because a few minutes of
// ordinary skew is not a false event — a year is.
func ahead(t, now time.Time) bool {
	// The window is half-open, as every other one here is: midnight belongs to
	// the day it opens, not to the one it closes.
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	return !t.Before(end)
}

// Path returns the usage log location for an index dir: a sibling file, like
// the .lock file, so it survives full index rebuilds.
func Path(indexDir string) string {
	return strings.TrimSuffix(indexDir, string(filepath.Separator)) + ".usage.jsonl"
}

// Record appends one event. Errors are swallowed on purpose.
func Record(indexDir, kind string, bytes int) {
	RecordResult(indexDir, kind, bytes, 0, false)
}

// RecordResult appends an event with result accounting. Errors are swallowed.
func RecordResult(indexDir, kind string, bytes, sessions int, empty bool) {
	RecordResultRaw(indexDir, kind, bytes, sessions, empty, 0)
}

// RecordResultRaw additionally stores the source-transcript size the digest
// was distilled from, for the served-vs-replayed ratio.
func RecordResultRaw(indexDir, kind string, bytes, sessions int, empty bool, raw int64) {
	recordFull(indexDir, kind, bytes, sessions, empty, raw, nil)
}

// RecordServedSessions is RecordResultRaw plus the ids of the sessions the
// digest contained.
func RecordServedSessions(indexDir, kind string, bytes, sessions int, empty bool, raw int64, ids []string) {
	recordFull(indexDir, kind, bytes, sessions, empty, raw, ids)
}

func recordFull(indexDir, kind string, bytes, sessions int, empty bool, raw int64, ids []string) {
	p := Path(indexDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	rotate(p)
	// O_RDWR rather than O_WRONLY: the append needs to read the last byte to
	// know whether the previous record finished (#1901).
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(Event{Time: time.Now().UTC(), Kind: kind, Bytes: bytes, Sessions: sessions, Empty: empty, RawBytes: raw, SessionIDs: ids})
	if err != nil {
		return
	}
	// A process killed between a record and its newline costs that record, which
	// is the trade this log makes for writing without a lock. It cost the next
	// one too: appended onto the partial line, so that line no longer parsed
	// either and a recall deja had just served went missing from every count
	// (#1901). One byte of reading closes the line first.
	if endsMidLine(f) {
		b = append([]byte{'\n'}, b...)
	}
	_, _ = f.Write(append(b, '\n'))
}

// endsMidLine reports whether the log's last byte is anything but a newline.
// A file that cannot be read is treated as ending cleanly: a usage event must
// never be the reason a recall fails.
func endsMidLine(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return false
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], fi.Size()-1); err != nil {
		return false
	}
	return last[0] != '\n'
}

// InjectedToday returns session-start context bytes injected since local midnight.
func InjectedToday(indexDir string) int {
	_, _, injected := TodayWithInjections(indexDir)
	return injected
}

// TodayWithInjections returns today's agent-memory events, served bytes, and
// the subset of those bytes injected by session-start hooks.
func TodayWithInjections(indexDir string) (recalls, bytes, injected int) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, e := range read(Path(indexDir)) {
		if e.Time.Before(midnight) || ahead(e.Time, now) {
			continue
		}
		switch {
		case servedKind(e.Kind):
			recalls++
			bytes += e.Bytes
		case injectedKind(e.Kind):
			recalls++
			bytes += e.Bytes
			injected += e.Bytes
		}
	}
	return recalls, bytes, injected
}

// TodayDemand returns today's non-empty, agent-requested memory events, the
// bytes they served, and separately the bytes deja injected unprompted.
// Automatic injections and empty results stay out of the recall count so
// headline counters use the same demand-side definition as Week.
//
// Injections come back from the same pass rather than from a second call: the
// statusline renders on every prompt, and two passes over the log can also
// straddle a write and report two numbers that were never true together.
func TodayDemand(indexDir string) (recalls, bytes, injected int) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, e := range read(Path(indexDir)) {
		if e.Time.Before(midnight) || ahead(e.Time, now) || e.Empty {
			continue
		}
		switch {
		case servedKind(e.Kind):
			recalls++
			bytes += e.Bytes
		case injectedKind(e.Kind):
			injected += e.Bytes
		}
	}
	return recalls, bytes, injected
}

// DejaVuWeek counts this week's déjà vu moments — prompts the user's own
// history already answered.
func DejaVuWeek(indexDir string) int {
	now := time.Now()
	cut := now.AddDate(0, 0, -7)
	n := 0
	for _, e := range read(Path(indexDir)) {
		if e.Kind == KindDejaVu && e.Time.After(cut) && !ahead(e.Time, now) && e.Sessions > 0 {
			n++
		}
	}
	return n
}

// TodayRaw sums the source-transcript volume behind today's served digests.
func TodayRaw(indexDir string) int64 {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var raw int64
	for _, e := range read(Path(indexDir)) {
		if e.Time.Before(midnight) || ahead(e.Time, now) {
			continue
		}
		if servedKind(e.Kind) || injectedKind(e.Kind) {
			raw += e.RawBytes
		}
	}
	return raw
}

// Totals summarizes the retained usage log.
func Totals(indexDir string) Summary {
	var out Summary
	empty := 0
	for _, e := range read(Path(indexDir)) {
		if out.Since.IsZero() || (!e.Time.IsZero() && e.Time.Before(out.Since)) {
			out.Since = e.Time
		}
		switch {
		case servedKind(e.Kind):
			out.Recalls++
			out.RecallSessions += e.Sessions
			out.Bytes += e.Bytes
			out.RawBytes += e.RawBytes
			if e.Empty {
				empty++
			}
		case injectedKind(e.Kind):
			out.Injections++
			out.InjectedSessions += e.Sessions
			out.InjectedBytes += e.Bytes
			out.Bytes += e.Bytes
			out.RawBytes += e.RawBytes
			if e.Kind == KindDejaVu {
				out.DejaVuMoments++
			}
		}
	}
	if out.Recalls > 0 {
		out.EmptyResultRate = float64(empty) / float64(out.Recalls)
	}
	return out
}

// Week aggregates the trailing seven days, split by who initiated: recalls
// counts only what the AGENT asked for and got (non-empty recall/context
// calls) — the honest demand-side number — while injected counts the hook
// deliveries deja pushed unprompted.
func Week(indexDir string) (recalls, bytes, injected, injectedBytes int) {
	now := time.Now()
	cut := now.Add(-7 * 24 * time.Hour)
	for _, e := range read(Path(indexDir)) {
		if e.Time.Before(cut) || ahead(e.Time, now) || e.Empty {
			continue
		}
		switch {
		case servedKind(e.Kind):
			recalls++
			bytes += e.Bytes
		case injectedKind(e.Kind):
			injected++
			injectedBytes += e.Bytes
		}
	}
	return recalls, bytes, injected, injectedBytes
}

// Today sums today's agent-memory events and their served bytes.
func Today(indexDir string) (recalls int, bytes int) {
	recalls, bytes, _ = TodayWithInjections(indexDir)
	return recalls, bytes
}

func read(p string) []Event {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []Event
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 1<<20)
	for s.Scan() {
		var e Event
		if json.Unmarshal(s.Bytes(), &e) == nil && !e.Time.IsZero() {
			out = append(out, e)
		}
	}
	return out
}

// rotate rewrites the log keeping only the recent window once it grows past
// rotateAt. An event dated ahead of the window is kept rather than dropped:
// the counters ignore it (see ahead), it costs one line, and deleting a
// recorded event on a guess about someone's clock is the one thing here that
// cannot be undone. Concurrent writers may lose an event during the swap; usage data
// is advisory, so that trade keeps the hot path lock-free.
//
// Size is the trigger, but the window is what actually bounds the file, and
// past 1MB the two disagree: a log that is large because it is busy — not
// because it is old — is over the trigger with nothing to drop, and then every
// recall rewrote the whole thing and left it exactly as long. Measured on a
// 10k-session store, per-recall cost went 123ms at an empty log to 342ms at
// 1.2MB and 870ms at 5.8MB, none of those rewrites dropping a single event.
// Reading the log to find that out is nearly free; writing it back is not, so
// the write is what gets skipped when every event survives the cutoff. Which
// event is oldest cannot be assumed from the order — a clock that steps back
// appends an older event behind a newer one, and dropping on that assumption
// would leave stale recalls weighing on ranking forever.
func rotate(p string) {
	fi, err := os.Stat(p)
	if err != nil || fi.Size() < rotateAt {
		return
	}
	cutoff := time.Now().UTC().Add(-keepWindow)
	all := read(p)
	var keep []Event
	for _, e := range all {
		if e.Time.After(cutoff) {
			keep = append(keep, e)
		}
	}
	if len(keep) == len(all) {
		return
	}
	// One buffer, then one atomic replace. The temp name used to be derived
	// from the log's own path, and this is the one writer in deja with no lock
	// at all by design — two rotations at once shared that name and could
	// publish a half-written log, which read as no history rather than as one
	// lost event (#1319).
	var buf bytes.Buffer
	for _, e := range keep {
		if b, err := json.Marshal(e); err == nil {
			buf.Write(append(b, '\n'))
		}
	}
	_ = atomicfile.Write(p, buf.Bytes(), 0o600)
}

// WornSessions counts, per session id, how often agent-initiated recalls
// served it inside the retention window. Search uses it as a small bounded
// boost: what agents keep pulling is what the user keeps needing.
func WornSessions(indexDir string) map[string]int {
	out := map[string]int{}
	for _, e := range read(Path(indexDir)) {
		// Agent recalls and the user's own déjà vu moments both count. They
		// mean different things — one is an agent pulling a session, the other
		// is the user returning to the same ground — and both say the session
		// keeps mattering. The bounded boost is what keeps this from becoming
		// a feedback loop: a session that ranks higher gets surfaced more,
		// which would compound without the ceiling in wornBoost.
		if e.Kind != KindRecall && e.Kind != KindContext && e.Kind != KindDejaVu {
			continue
		}
		for _, id := range e.SessionIDs {
			out[id]++
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ImpactReport assembles the measured proof that recall changes outcomes.
// Every number is counted from recorded events on this machine — nothing is
// modeled, sampled, or estimated.
type ImpactReport struct {
	Recalls       int   `json:"recalls"`        // agent-initiated, non-empty
	Injections    int   `json:"injections"`     // session starts that began with memory
	ServedBytes   int   `json:"served_bytes"`   // digest bytes actually returned
	RawBytes      int64 `json:"raw_bytes"`      // source transcripts those digests distilled
	ReusedTwice   int   `json:"reused_twice"`   // sessions agents recalled 2+ times
	DejaVuMoments int   `json:"dejavu_moments"` // prompts matched to prior work
	// Since is the oldest event still in the log. The log is rewritten past
	// 1MB keeping the last 14 days, so every count above is a window and not a
	// lifetime — one rotation over a 30-day log halved them (#1889). The same
	// fact `deja stats` has carried since #763.
	Since time.Time `json:"-"`
}

// MarshalJSON writes Since only when there is one, for the reason Summary's
// does: `omitempty` does nothing to a struct, so the tag alone would print
// January of year 1 on a machine with no recall history (#1874).
func (r ImpactReport) MarshalJSON() ([]byte, error) {
	type plain ImpactReport
	out := struct {
		plain
		Since *time.Time `json:"since,omitempty"`
	}{plain: plain(r)}
	if !r.Since.IsZero() {
		out.Since = &r.Since
	}
	return json.Marshal(out)
}

// Impact counts across the whole usage log.
func Impact(indexDir string) ImpactReport {
	var r ImpactReport
	worn := map[string]int{}
	for _, e := range read(Path(indexDir)) {
		if r.Since.IsZero() || e.Time.Before(r.Since) {
			r.Since = e.Time
		}
		switch {
		case servedKind(e.Kind):
			if e.Empty {
				continue
			}
			r.Recalls++
			r.ServedBytes += e.Bytes
			r.RawBytes += e.RawBytes
			for _, id := range e.SessionIDs {
				worn[id]++
			}
		case e.Kind == KindHook:
			// A session start with no project session to show still injects
			// the environment block, and that event is logged empty. Counting
			// it made "N session starts began with project memory" claim
			// memory that was not there.
			if e.Empty {
				continue
			}
			r.Injections++
			r.ServedBytes += e.Bytes
			r.RawBytes += e.RawBytes
		case e.Kind == KindDejaVu:
			r.DejaVuMoments++
		}
	}
	for _, n := range worn {
		if n >= 2 {
			r.ReusedTwice++
		}
	}
	return r
}
