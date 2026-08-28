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
	"sort"
	"strings"
	"sync"
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

// FoundNothing reports whether a lookup came back with nothing, which is what
// `deja log` marks. Not the same as the Empty flag: on an injection the flag
// says no project session went in, and a session start on a checkout with no
// sessions of its own still injects the environment block — bytes and all
// (#1954). On a lookup the flag does mean the answer found nothing, which is
// why an empty recall still serves the sentence that says so.
func (e Event) FoundNothing() bool { return e.Empty && !injectedKind(e.Kind) }

type Event struct {
	Time     time.Time `json:"t"`
	Kind     string    `json:"kind"`
	Bytes    int       `json:"bytes"`
	Sessions int       `json:"sessions,omitempty"`
	// Empty means no session went into the event, which is not the same as
	// serving nothing: a session start on a checkout with no sessions of its
	// own still injects the environment block, and that event carries its
	// bytes (#1954).
	Empty bool `json:"empty,omitempty"`
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
	// keepAtLeast is how many events survive when every one of them is older
	// than the window. Without it, a fortnight away emptied the log on the
	// first recall back and the impact screen said no recall had ever happened
	// (#1922). Measured at 59.8 KB, against the megabyte that triggers a
	// rewrite.
	keepAtLeast = 200
	// EventRoom is how large one event may be for the fallback above to leave
	// the file under its threshold: keepAtLeast of them have to fit in
	// rotateAt. A recall over short sessions with filename-length ids writes
	// 2.9 kB of it, which is the widest a writer produces today.
	//
	// It bounds that fallback and not the ordinary rotation, which keeps
	// whatever is inside the window and can leave the file over the threshold
	// with no size rule at all. That state is transient — the next write finds
	// nothing to drop and stops re-reading (#1972) — and closing it properly is
	// the retention question that issue leaves open. The injection log took the
	// other road in #1971 and drops the oldest until the rebuild fits.
	EventRoom = 4096
	// memoWindow is how long a read's finding answers for later appends. Short
	// enough that an event arriving with an old stamp waits minutes rather than
	// a fortnight, long enough that a busy process pays the read rarely.
	memoWindow = 5 * time.Minute
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
	recordFullAt(indexDir, kind, bytes, sessions, empty, raw, ids, time.Now().UTC())
}

// recordFullAt is recordFull with the instant supplied, so an injection that
// writes an event AND a digest snapshot stamps both with one time. Two
// time.Now() calls left the two logs disagreeing by microseconds about the same
// injection, and nothing else joins them (#2294).
func recordFullAt(indexDir, kind string, bytes, sessions int, empty bool, raw int64, ids []string, at time.Time) {
	p := Path(indexDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	rotate(p)
	// The call above is not a guard: another process can rotate between this
	// open and the write below, leaving this descriptor on the file that was
	// just retired, and the event goes with it. Accepted, measured — it needs a
	// log past 1MB and a rotation inside the open, the stat and the write, tens
	// of microseconds here and more under load. Closing it means locking a file
	// this package appends to without one by design (#1319). One event is the
	// cost, and one event is what this file already risks on a crash.
	//
	// O_RDWR rather than O_WRONLY: the append needs to read the last byte to
	// know whether the previous record finished (#1901).
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(Event{Time: at, Kind: kind, Bytes: bytes, Sessions: sessions, Empty: empty, RawBytes: raw, SessionIDs: ids})
	if err != nil {
		return
	}
	// A process killed between a record and its newline costs that record, which
	// is the trade this log makes for writing without a lock. It cost the next
	// one too: appended onto the partial line, so that line no longer parsed
	// either and a recall deja had just served went missing from every count
	// (#1901). One byte of reading closes the line first.
	if atomicfile.EndsMidLine(f) {
		b = append([]byte{'\n'}, b...)
	}
	_, _ = f.Write(append(b, '\n'))
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

// StatusNumbers is everything the status line can print, from one read.
type StatusNumbers struct {
	// Today, demand side: what an agent asked for and got, and what deja
	// injected unprompted, on the same terms TodayDemand uses.
	Recalls  int
	Bytes    int
	Injected int
	// This week, for the line the quiet days print.
	WeekRecalls int
	WeekBytes   int
	// Today's source transcripts behind what was served, for the "less than
	// replaying" clause.
	RawToday int64
}

// StatusCounters is TodayDemand, Week and TodayRaw in one pass.
//
// The line renders on every prompt and took two of these reads — 8 ms each on a
// busy fortnight's log — for numbers one read produces. TodayDemand's own doc
// gives the other half of the reason: two passes can straddle a write and
// report numbers that were never true together, and the line prints today's
// beside the week's (#2224).
func StatusCounters(indexDir string) StatusNumbers {
	var out StatusNumbers
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	cut := WeekCut(now)
	for _, e := range read(Path(indexDir)) {
		if ahead(e.Time, now) {
			continue
		}
		// TodayRaw counts what was served today whether or not it found
		// anything, which is the one place the empty rule differs.
		if !e.Time.Before(midnight) && (servedKind(e.Kind) || injectedKind(e.Kind)) {
			out.RawToday += e.RawBytes
		}
		if e.FoundNothing() {
			continue
		}
		if !e.Time.Before(midnight) {
			switch {
			case servedKind(e.Kind):
				out.Recalls++
				out.Bytes += e.Bytes
			case injectedKind(e.Kind):
				out.Injected += e.Bytes
			}
		}
		if !e.Time.Before(cut) && servedKind(e.Kind) {
			out.WeekRecalls++
			out.WeekBytes += e.Bytes
		}
	}
	return out
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
		// FoundNothing, not the raw flag: on an injection the flag means no
		// project session went in, and the environment block goes out with it
		// — dropping those said "0 B injected" on a day memory had been
		// arriving since morning (#1962).
		if e.Time.Before(midnight) || ahead(e.Time, now) || e.FoundNothing() {
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

// WeekCut is when "this week" opens: seven calendar days back, at the same wall
// time — or, in the hour a spring-forward removes, at the time the clock
// actually reached, since 02:30 did not happen that day. Not 168 hours — in a zone with daylight saving those differ by an hour
// for one week in each direction, and the status bar prints the week counters
// beside the déjà-vu count, so one of them counted an event the other did not
// (#1920). The day counters here cut at local midnight and the brief cuts at
// seven calendar days, so this is the rule the rest of deja already speaks.
func WeekCut(now time.Time) time.Time {
	return now.AddDate(0, 0, -7)
}

// DejaVuWeek counts this week's déjà vu moments — prompts the user's own
// history already answered.
func DejaVuWeek(indexDir string) int {
	now := time.Now()
	cut := WeekCut(now)
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
	cut := WeekCut(now)
	for _, e := range read(Path(indexDir)) {
		// Same rule as the day: the week that contains today has to contain
		// today's injected bytes (#1962).
		if e.Time.Before(cut) || ahead(e.Time, now) || e.FoundNothing() {
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

// usable reports whether a line is an event at all: a stamp, so it can be
// placed in time, and a kind, so it can be counted or named. The two readers
// asked for one each — the counters for a stamp, `deja log` for a kind — so a
// half-written line was a row on one surface and nothing on the other, and a
// line with no kind, which no counter has a case for, still set `since`, the
// window every figure on the impact screen is measured from (#1917).
//
// An unrecognised kind stays an event: deja may have written it, an older or a
// newer version of itself, and it belongs in the log the reader browses even
// though no counter has a case for it.
func (e Event) usable() bool {
	return !e.Time.IsZero() && e.Kind != ""
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
		if json.Unmarshal(s.Bytes(), &e) == nil && e.usable() {
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
// The write is what gets skipped when every event survives the cutoff. The read
// that decides it is not free either — on a busy fortnight it is the whole file
// on every event, 6.4 ms against 28 µs — so what one read found answers for the
// next few minutes (#1972). Which
// event is oldest cannot be assumed from the order — a clock that steps back
// appends an older event behind a newer one, and dropping on that assumption
// would leave stale recalls weighing on ranking forever.
func rotate(p string) {
	fi, err := os.Stat(p)
	if err != nil || fi.Size() < rotateAt {
		return
	}
	if skipRotation(p, fi.Size()) {
		return
	}
	cutoff := time.Now().UTC().Add(-keepWindow)
	// One bit decides whether this write pays for a rotation: is any event old
	// enough to drop. Parsing the whole log to learn it cost every short-lived
	// hook 13 ms on a log of twelve thousand events, and the memo below only
	// spares the second write in the same process (#2220).
	oldest, ok := oldestStampIn(p)
	if ok && !oldest.Before(cutoff) {
		rememberOldest(p, oldest, fi.Size())
		return
	}
	all := read(p)
	var keep []Event
	for _, e := range all {
		if e.Time.After(cutoff) {
			keep = append(keep, e)
		}
	}
	if len(keep) == len(all) {
		rememberNothingToDrop(p, all, fi.Size())
		return
	}
	if len(keep) == 0 && len(all) > 0 {
		// Newest first, then back into the order the file is read in.
		// Stable, so two events written in the same second keep the order they
		// were written in — that order is the only thing separating them, and
		// `deja log` prints it.
		byTime := append([]Event(nil), all...)
		sort.SliceStable(byTime, func(i, j int) bool { return byTime[i].Time.After(byTime[j].Time) })
		if len(byTime) > keepAtLeast {
			byTime = byTime[:keepAtLeast]
		}
		sort.SliceStable(byTime, func(i, j int) bool { return byTime[i].Time.Before(byTime[j].Time) })
		keep = byTime
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

// oldestStampIn reads the log's stamps without decoding its records. Every line
// this package writes starts with {"t":"<RFC3339>", so the oldest event can be
// found by comparing text.
//
// To the second, deliberately. RFC3339Nano drops trailing zeros from the
// fraction, so "…:00.5Z" and "…:00Z" differ first at '.' against 'Z' and sort
// the wrong way round — the fraction is exactly where text order stops being
// time order. Seconds are all this decides with: the question is whether
// anything is older than a fortnight.
//
// ok is false when a line does not carry a stamp where the writer puts it: a
// log from another version, a half-written line, anything this cannot speak
// for. The caller then does what it always did and parses.
func oldestStampIn(p string) (oldest time.Time, ok bool) {
	f, err := os.Open(p)
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	best := ""
	sc := bufio.NewScanner(f)
	// The same line ceiling read() uses: a line longer than that is one this
	// scan cannot speak for either.
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		stamp, found := stampPrefix(line)
		if !found {
			return time.Time{}, false
		}
		if best == "" || stamp[:len(secondsPrecision)] < best[:len(secondsPrecision)] {
			best = stamp
		}
	}
	if sc.Err() != nil || best == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, best)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// stampPrefix returns the value of the leading "t" field, which is where this
// package's writer puts it. Anything else is left to the parser.
func stampPrefix(line string) (string, bool) {
	const prefix = `{"t":"`
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rest := line[len(prefix):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", false
	}
	stamp := rest[:end]
	// Only the shape the writer emits: UTC, with the trailing Z, and long
	// enough to carry whole seconds. An offset like +02:00 sorts by its digits
	// rather than by when it happened.
	if len(stamp) < len(secondsPrecision) || !strings.HasSuffix(stamp, "Z") {
		return "", false
	}
	return stamp, true
}

// secondsPrecision is the prefix of an RFC 3339 stamp up to whole seconds —
// "2026-08-27T10:00:00" — and its length is how much of one this compares.
const secondsPrecision = "2006-01-02T15:04:05"

// rememberOldest records what the scan above found, in the same shape the full
// read records, so the memo means one thing however it was filled.
func rememberOldest(p string, oldest time.Time, size int64) {
	nothingToDrop.Store(p, rotationMemo{oldest: oldest, size: size, at: time.Now().UTC()})
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
		case injectedKind(e.Kind):
			// A session start with no project session to show still injects
			// the environment block, and that event is logged empty. Counting
			// it made "N session starts began with project memory" claim
			// memory that was not there.
			//
			// The bytes go with the count, which is the part that looks like
			// an oversight and is not. The block is a summary of what this
			// machine keeps hitting, not a digest of transcripts, so it is
			// recorded with no raw size behind it. Adding it to ServedBytes
			// alone divides a real numerator by an unchanged denominator:
			// measured on three recalls and ten blocks, a tenfold saving reads
			// as fourfold, understating what deja did. Both stay out.
			if e.Empty {
				if e.Kind == KindDejaVu {
					r.DejaVuMoments++
				}
				continue
			}
			// Every door that carried a digest, not the session-start one
			// alone: the per-prompt recall and the tool-time line are distilled
			// from real transcripts too, and dropping them computed the ratio
			// this report exists for from half the events — the drift #1907
			// fixed for blame, running the other way (#2204).
			if e.Kind == KindDejaVu {
				r.DejaVuMoments++
			}
			// `injections` keeps the meaning the report documents: session
			// starts that began with project memory.
			if e.Kind == KindHook {
				r.Injections++
			}
			r.ServedBytes += e.Bytes
			r.RawBytes += e.RawBytes
		}
	}
	for _, n := range worn {
		if n >= 2 {
			r.ReusedTwice++
		}
	}
	return r
}

// nothingToDrop remembers, per log, what the last full read found: the oldest
// event in it, and the size the file had then. Both are needed — the stamp says
// when a rotation could next drop something, the size says the file is still
// the one that was read.
var nothingToDrop sync.Map // path -> rotationMemo

type rotationMemo struct {
	oldest time.Time
	size   int64
	at     time.Time // when this was learned
}

// skipRotation reports whether the last read of this log already established
// that nothing would age out, and nothing has happened since to change that.
//
// It is only worth anything to a process that records more than once — the MCP
// server answering recalls all day. A hook runs once and exits, so it pays the
// read the first time either way.
func skipRotation(p string, size int64) bool {
	v, ok := nothingToDrop.Load(p)
	if !ok {
		return false
	}
	m := v.(rotationMemo)
	now := time.Now().UTC()
	// A memo is about the events one read saw. An event stamped in the past —
	// a clock that stepped back, a log carried from another machine — arrives
	// behind it and would otherwise wait out the whole window before anything
	// looked again. A few minutes bounds that without giving back the cost.
	if now.Sub(m.at) > memoWindow {
		return false
	}
	if !m.oldest.After(now.Add(-keepWindow)) {
		return false // the oldest event has aged out since
	}
	// Only growth is expected: a file that shrank was rewritten by someone
	// else, and what this process remembers about it is about the old one.
	return size >= m.size
}

// rememberNothingToDrop records what a full read found, so the next append does
// not repeat it.
func rememberNothingToDrop(p string, all []Event, size int64) {
	oldest := time.Time{}
	for _, e := range all {
		if oldest.IsZero() || e.Time.Before(oldest) {
			oldest = e.Time
		}
	}
	if oldest.IsZero() {
		return
	}
	nothingToDrop.Store(p, rotationMemo{oldest: oldest, size: size, at: time.Now().UTC()})
}
