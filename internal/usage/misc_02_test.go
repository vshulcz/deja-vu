package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A blank line is what two processes leave when both close a truncated tail
// (#1902), and it is not corruption: the events around it are whole. Both
// readers here drop a line that does not parse, which covers it — an empty line
// is not valid JSON — but nothing said so, and a blank line in a JSON-lines
// file is exactly the kind of thing a later reader makes fatal.
func TestBlankLinesAreReadPast(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	one := func(bytes int) string {
		b, err := json.Marshal(Event{
			Time: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC), Kind: KindRecall,
			Bytes: bytes, Sessions: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := strings.Join([]string{one(100), "", one(100), "", "", one(100)}, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if tot := Totals(dir); tot.Recalls != 3 || tot.Bytes != 300 {
		t.Errorf("blank lines cost events: %d recalls, %d bytes", tot.Recalls, tot.Bytes)
	}
	if imp := Impact(dir); imp.Recalls != 3 {
		t.Errorf("the impact screen counts %d of the three", imp.Recalls)
	}
	// `deja log` reads the same file through its own scanner, which keeps a
	// line on a different condition (a kind rather than a stamp), so it is
	// asserted here rather than assumed to follow.
	if evs := Events(dir, 0); len(evs) != 3 {
		t.Errorf("deja log reads %d of the three events", len(evs))
	}

	// And a record appended to such a file lands on its own line, as it would
	// on any other.
	RecordResult(dir, KindRecall, 200, 1, false)
	if tot := Totals(dir); tot.Recalls != 4 || tot.Bytes != 500 {
		body, _ := os.ReadFile(p)
		t.Errorf("appending to a log with blank lines lost something: %d recalls, %d bytes\n%s",
			tot.Recalls, tot.Bytes, body)
	}
}

// Rotation walks the same reader, so it must not choke on them either — and
// what it writes back has none, since it is written from the events.
func TestRotationSurvivesBlankLines(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var b strings.Builder
	for i := range 4000 {
		age := time.Duration(i%30) * 24 * time.Hour
		e := Event{Time: now.Add(-age), Kind: KindRecall, Bytes: 100, Sessions: 1,
			SessionIDs: []string{strings.Repeat("x", 200)}}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteString("\n\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(read(p))
	rotate(p)
	after := len(read(p))
	if after == 0 || after >= before {
		t.Fatalf("rotation of a log with blank lines kept %d of %d events", after, before)
	}
	rewritten, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "\n\n") {
		t.Errorf("the rotated log carries blank lines forward")
	}
	if evs := Events(dir, 0); len(evs) != after {
		t.Errorf("deja log reads %d events from the rotated log, the counters read %d", len(evs), after)
	}
}

// The brief printed four figures and walked the log once per figure, and the
// week note walked it twice for two (#1576). Both read from one pass now, so
// what the pass reports has to be what the separate readers reported — and the
// two counters that had no field on StatusNumbers have to be right on their own
// terms, not merely consistent with a wrapper that now returns them.

func weekFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today := func(ago time.Duration) time.Time {
		if at := now.Add(-ago); !at.Before(midnight) {
			return at
		}
		return midnight.Add(time.Second)
	}
	// Three déjà vu moments inside the week, of which one found nothing behind
	// it and is not a moment at all.
	appendEventForTest(t, dir, Event{Time: today(time.Hour), Kind: KindDejaVu, Sessions: 3})
	appendEventForTest(t, dir, Event{Time: now.Add(-48 * time.Hour), Kind: KindDejaVu, Sessions: 1})
	appendEventForTest(t, dir, Event{Time: today(2 * time.Hour), Kind: KindDejaVu, Sessions: 0})
	// One older than the week, which is not this week's news.
	appendEventForTest(t, dir, Event{Time: now.Add(-30 * 24 * time.Hour), Kind: KindDejaVu, Sessions: 4})
	// Two unprompted arrivals inside the week, and a recall beside them.
	appendEventForTest(t, dir, Event{Time: today(3 * time.Hour), Kind: KindHook, Bytes: 500, Sessions: 2})
	appendEventForTest(t, dir, Event{Time: now.Add(-24 * time.Hour), Kind: KindHook, Bytes: 700, Sessions: 1})
	appendEventForTest(t, dir, Event{Time: today(4 * time.Hour), Kind: KindRecall, Bytes: 900, Sessions: 2, RawBytes: 9000})
	return dir
}

func TestOnePassCountsThisWeeksDejaVuAndArrivals(t *testing.T) {
	dir := weekFixture(t)
	got := StatusCounters(dir)
	if got.DejaVuWeek != 2 {
		t.Errorf("déjà vu this week = %d, want 2 (the third had no sessions behind it, the fourth is older than the week)", got.DejaVuWeek)
	}
	// Five, not two: a déjà vu moment is an injected kind, so the three inside
	// the week are arrivals as well — including the one with no sessions
	// behind it, since the empty rule does not apply to an injection. This is
	// what Week() counted before, and the point of the field is to keep
	// counting it.
	if got.WeekInjections != 5 {
		t.Errorf("arrivals this week = %d, want 5", got.WeekInjections)
	}
	// Only the two hooks carried bytes; a déjà vu moment is a notice.
	if got.WeekInjectedBytes != 1200 {
		t.Errorf("arrival bytes this week = %d, want 1200", got.WeekInjectedBytes)
	}
	if got.WeekRecalls != 1 || got.WeekBytes != 900 {
		t.Errorf("week recalls = %d/%d, want 1/900", got.WeekRecalls, got.WeekBytes)
	}
}

// The readers the brief and the week note used to call are wrappers now. Each
// has to return its own figures — a wrapper wired to the wrong field would
// still compile and still look consistent with itself.
func TestTheWrappersReturnTheirOwnFigures(t *testing.T) {
	dir := weekFixture(t)
	got := StatusCounters(dir)

	if dv := DejaVuWeek(dir); dv != got.DejaVuWeek {
		t.Errorf("DejaVuWeek = %d, one pass says %d", dv, got.DejaVuWeek)
	}
	recalls, bytes, injected := TodayDemand(dir)
	if recalls != got.Recalls || bytes != got.Bytes || injected != got.Injected {
		t.Errorf("TodayDemand = %d/%d/%d, one pass says %d/%d/%d",
			recalls, bytes, injected, got.Recalls, got.Bytes, got.Injected)
	}
	wr, wb, wi, wib := Week(dir)
	if wr != got.WeekRecalls || wb != got.WeekBytes || wi != got.WeekInjections || wib != got.WeekInjectedBytes {
		t.Errorf("Week = %d/%d/%d/%d, one pass says %d/%d/%d/%d",
			wr, wb, wi, wib, got.WeekRecalls, got.WeekBytes, got.WeekInjections, got.WeekInjectedBytes)
	}
	if raw := TodayRaw(dir); raw != got.RawToday {
		t.Errorf("TodayRaw = %d, one pass says %d", raw, got.RawToday)
	}
	// Premise: none of this is zero, or agreement measures nothing.
	if got.Recalls == 0 || got.WeekRecalls == 0 || got.RawToday == 0 || got.DejaVuWeek == 0 || got.WeekInjections == 0 {
		t.Fatalf("the fixture produced %+v, so agreement measures nothing", got)
	}
}

// A log the size of a busy fortnight: the issue measured 3,162 lines / 324 KB
// on a real machine, and rotation keeps 14 days or 1 MB.
func benchLog(b *testing.B, lines int) string {
	b.Helper()
	dir := b.TempDir()
	f, err := os.OpenFile(Path(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	now := time.Now()
	kinds := []string{KindRecall, KindHook, KindContext, KindDejaVu}
	for i := range lines {
		// Spread over the retained window, so most lines fall outside the day
		// the counters keep — which is the work a pass does and throws away.
		e := Event{
			Time:     now.Add(-time.Duration(i) * 6 * time.Minute),
			Kind:     kinds[i%len(kinds)],
			Bytes:    100 + i%900,
			Sessions: 1 + i%3,
			RawBytes: int64(1000 + i%9000),
		}
		line, err := json.Marshal(e)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			b.Fatal(err)
		}
	}
	return dir
}

// Before: the four figures the brief prints, one reader each.
func BenchmarkBriefFiguresSeparatePasses(b *testing.B) {
	dir := benchLog(b, 3200)
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TodayDemand(dir)
		_, _, _, _ = Week(dir)
		_ = DejaVuWeek(dir)
		_ = TodayRaw(dir)
	}
}

// After: the same four figures, one walk.
func BenchmarkBriefFiguresOnePass(b *testing.B) {
	dir := benchLog(b, 3200)
	b.ResetTimer()
	for b.Loop() {
		_ = StatusCounters(dir)
	}
}

// Since #2472 the reader's own searches land in the log too, and someone who
// lives in the CLI writes far more of them than the machine writes injections.
// The window that rotation keeps is a window in time, not a count, so the
// searches must not crowd a fortnight of session starts out of what the impact
// screen reports.
func TestASearchFloodDoesNotEvictTheFortnightOfStarts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	path := Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var lines []string
	starts := 0
	for day := 13; day >= 0; day-- {
		for k := range 2 {
			e := Event{Time: now.Add(-time.Duration(day)*24*time.Hour - time.Duration(k)*time.Hour),
				Kind: KindHook, Bytes: 1800, Sessions: 3, RawBytes: 21000}
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(b))
			starts++
		}
		for i := range 1000 {
			e := Event{Time: now.Add(-time.Duration(day)*24*time.Hour - time.Duration(i)*time.Second),
				Kind: KindSearch, Bytes: 5200, Sessions: 12}
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(b))
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < rotateAt {
		t.Fatalf("the fixture is meant to be over the rotation size; it is %d bytes", fi.Size())
	}
	rotate(path)

	imp := Impact(dir)
	if imp.Injections != starts {
		t.Errorf("impact reports %d session starts of the %d in the log", imp.Injections, starts)
	}
	if imp.Recalls != 0 {
		t.Errorf("searches are counted as recalls served: %d", imp.Recalls)
	}
	if got := Totals(dir); got.Injections != starts {
		t.Errorf("stats reports %d session starts of %d", got.Injections, starts)
	}
	// The oldest event decides the period every count on that screen covers.
	if age := time.Since(imp.Since); age < 13*24*time.Hour {
		t.Errorf("the reported period covers only %s, so the oldest starts were dropped", age.Round(time.Hour))
	}
}

// `RecordSize` allows 512 bytes for the envelope — the stamp, the kind, the
// policy name, the terms, the receiving session — and every case that measures
// the escaping uses a body big enough that 3n swamps that half. This measures
// the envelope on its own.
//
// The terms are the part that grows: a prompt containing one long word puts
// that word in the record, and `prompt.Terms` treats a path or a hash as a
// single token (#1988).
func TestTheEnvelopeFitsWhatItIsAllowed(t *testing.T) {
	body := "a digest"
	for _, c := range []struct {
		name string
		snap Snapshot
	}{
		{"a bare record", Snapshot{Time: time.Now().UTC(), Kind: KindHook, Digest: body}},
		{"every field at a plausible size", Snapshot{
			Time: time.Now().UTC(), Kind: KindRecall, Sessions: 99, Bytes: len(body),
			Policy: "local+imported+promoted+whatever-a-policy-name-can-be",
			Terms:  []string{"pgbouncer", "transaction", "prepared", "statements", "timeout", "retry"},
			Into:   strings.Repeat("s", 128), Digest: body,
		}},
		{"terms from a prompt holding a long path", Snapshot{
			Time: time.Now().UTC(), Kind: KindDejaVu, Sessions: 2, Bytes: len(body),
			Terms: []string{strings.Repeat("pgbouncer/", 400), "timeout"}, Digest: body,
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := marshalSnapshot(c.snap)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(b); n > RecordSize(len(body)) {
				t.Errorf("record is %d bytes for a digest of %d; RecordSize says %d",
					n, len(body), RecordSize(len(body)))
			}
		})
	}
}

// The events log takes concurrent writers with no lock on purpose: usage data
// is advisory and the hot path stays lock-free, so a rotation running against
// an append may drop that event (#1319). What is not on the table is a log that
// cannot be read: a half-written line reads as no history rather than as one
// lost event, and a duplicated event would inflate the numbers every surface
// quotes. This pins the part that has to hold (#2425).
//
// The log is seeded with events old enough to drop, because a rotation with
// nothing to drop returns before it writes anything (#1972) — without the seed
// this exercises appends alone and says nothing about a rewrite.
func TestTheEventsLogStaysReadableUnderWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	seeded := seedOldEvents(t, dir)

	wide := strings.Repeat("id", 400)
	const writers, each = 4, 200
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range each {
				RecordServedSessions(dir, KindSearch, 4000, 1, false, 40000,
					[]string{fmt.Sprintf("%s-w%d-%d", wide, w, i)})
			}
		}(w)
	}
	wg.Wait()

	body, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	stale := 0
	for i, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		// A blank line is not a lost record: the writers close a line they
		// find open before appending (#1901), and a rotation landing between
		// that check and the write leaves the newline with nothing after it.
		// The reader skips what it cannot parse, so an empty line costs
		// nothing — what would cost something is a record cut in half, which
		// is a non-empty line that will not parse.
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is a record cut in half: %v\n%.200s", i+1, err, line)
		}
		if strings.HasPrefix(e.Kind, "seed") {
			stale++
			continue
		}
		for _, id := range e.SessionIDs {
			seen[id]++
		}
	}
	// The premise: a rotation actually rewrote the file. Some of the seed is
	// expected to survive — when every event is past the window the newest
	// keepAtLeast are kept, so that a fortnight away does not empty the log
	// (#1922) — but the file is smaller than what was seeded.
	if fi, err := os.Stat(Path(dir)); err != nil {
		t.Fatal(err)
	} else if fi.Size() >= seeded {
		t.Fatalf("the log did not shrink (%d B seeded, %d B now), so no rewrite raced an append", seeded, fi.Size())
	}
	if stale > keepAtLeast {
		t.Errorf("%d events past the window survived, more than the %d floor", stale, keepAtLeast)
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("an event was written %d times: %.40s", n, id)
		}
	}
	// How much the trade actually costs, recorded rather than asserted: this
	// machine kept all 800, CI kept 200 and 362 of them on the two runners.
	// The comment in rotate says an event "may" be lost; against a rotation
	// that is rewriting, it is most of a burst. There is no bound to hold here
	// — the design promises none — and a floor written from one machine's
	// timing is a flake waiting for a slower one.
	t.Logf("%d of %d appended events survived a rotation running against them", len(seen), writers*each)
}

// seedOldEvents fills the log past the rotation threshold with events dated
// beyond the keep window, so the next append has something to drop.
func seedOldEvents(t *testing.T, dir string) int64 {
	t.Helper()
	old := time.Now().UTC().Add(-keepWindow - 48*time.Hour)
	var buf bytes.Buffer
	for i := 0; buf.Len() < rotateAt+(1<<16); i++ {
		e := Event{Time: old.Add(time.Duration(i) * time.Second), Kind: "seed", Bytes: 4000,
			Sessions: 1, SessionIDs: []string{strings.Repeat("s", 800) + strconv.Itoa(i)}}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(b, '\n'))
	}
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return int64(buf.Len())
}

// The injection log is append-only so agents can write to it at once. A forget
// sweep reads the whole file, drops what matched and writes the rest back, and
// anything appended in between was written over — the record of an injection an
// agent had already received, gone with no sign (#2413).
func TestAnInjectionAppendedDuringAForgetSurvivesIt(t *testing.T) {
	for round := range 3 {
		dir := filepath.Join(t.TempDir(), "index.db")
		for i := range 300 {
			RecordDigestInto(dir, "hook", fmt.Sprintf("digest for gone %d", i), "gone-"+strconv.Itoa(i), 1, 100, []string{"t"})
		}
		const appended = 200
		var wg sync.WaitGroup
		wg.Go(func() {
			for i := range appended {
				RecordDigestInto(dir, "hook", fmt.Sprintf("digest for keep %d", i), "keep-"+strconv.Itoa(i), 1, 100, []string{"t"})
			}
		})
		gone, err := ForgetSnapshots(dir, func(s Snapshot) bool {
			return strings.Contains(s.Digest, "gone")
		})
		wg.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if gone == 0 {
			t.Fatalf("round %d: the sweep matched nothing, so it never rewrote the file", round)
		}
		kept := 0
		for _, s := range Snapshots(dir, 0) {
			if strings.Contains(s.Digest, "keep") {
				kept++
			}
		}
		if kept != appended {
			t.Errorf("round %d: %d of %d injections were written over by the sweep", round, appended-kept, appended)
		}
	}
}

func appendAt(t *testing.T, dir string, at time.Time, kind string, bytes int) {
	t.Helper()
	f, err := os.OpenFile(Path(dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(Event{Time: at.UTC(), Kind: kind, Bytes: bytes, Sessions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// The counters are windows on the recent past, and an event dated ahead of the
// window sits inside every one of them. A clock that ran fast before it was
// corrected leaves one behind, and the rotation that bounds the log keeps
// whatever is newer than its cutoff — so it was counted in "today" every day
// until its own date arrived.
func TestCountersIgnoreAnEventFromTheFuture(t *testing.T) {
	dir := t.TempDir()
	Record(dir, KindRecall, 900)
	appendAt(t, dir, time.Now().AddDate(1, 0, 0), KindRecall, 4000)

	if r, b, _ := TodayWithInjections(dir); r != 1 || b != 900 {
		t.Errorf("today counted the future event: %d recalls, %d bytes", r, b)
	}
	if r, b, _, _ := Week(dir); r != 1 || b != 900 {
		t.Errorf("this week counted the future event: %d recalls, %d bytes", r, b)
	}
}

func TestDejaVuWeekIgnoresTheFuture(t *testing.T) {
	dir := t.TempDir()
	appendAt(t, dir, time.Now().Add(-time.Hour), KindDejaVu, 400)
	appendAt(t, dir, time.Now().AddDate(0, 1, 0), KindDejaVu, 400)
	if n := DejaVuWeek(dir); n != 1 {
		t.Errorf("DejaVuWeek = %d, want 1", n)
	}
}

func TestTodayRawIgnoresTheFuture(t *testing.T) {
	dir := t.TempDir()
	RecordResultRaw(dir, KindRecall, 900, 1, false, 5000)
	f, err := os.OpenFile(Path(dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(Event{Time: time.Now().UTC().AddDate(1, 0, 0), Kind: KindRecall, Bytes: 4000, RawBytes: 90000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got := TodayRaw(dir); got != 5000 {
		t.Errorf("TodayRaw = %d, want 5000", got)
	}
}

// Ordinary skew is not a false event: something stamped later today still
// counts, and so does the last moment of the day.
func TestOrdinarySkewStillCounts(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	appendAt(t, dir, endOfDay, KindRecall, 700)
	if r, _, _ := TodayWithInjections(dir); r != 1 {
		t.Errorf("an event stamped later today was dropped: %d recalls", r)
	}
	if r, _, _, _ := Week(dir); r != 1 {
		t.Errorf("this week dropped it too: %d recalls", r)
	}
}

// The window is half-open: an event stamped at the midnight that ends today
// belongs to tomorrow, and one a microsecond before it belongs to today.
func TestTheDayBoundaryIsHalfOpen(t *testing.T) {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)

	inside := t.TempDir()
	appendAt(t, inside, end.Add(-time.Microsecond), KindRecall, 700)
	if r, _, _ := TodayWithInjections(inside); r != 1 {
		t.Errorf("the last instant of today was dropped: %d recalls", r)
	}

	outside := t.TempDir()
	appendAt(t, outside, end, KindRecall, 700)
	if r, _, _ := TodayWithInjections(outside); r != 0 {
		t.Errorf("tomorrow's first instant counted as today: %d recalls", r)
	}
}

// Totals still report what the log holds: the guard is on the windows, not on
// the record.
func TestTotalsStillSeeTheFutureEvent(t *testing.T) {
	dir := t.TempDir()
	Record(dir, KindRecall, 900)
	appendAt(t, dir, time.Now().AddDate(1, 0, 0), KindRecall, 4000)
	if got := Totals(dir).Recalls; got != 2 {
		t.Errorf("Totals hid a recorded event: %d, want 2", got)
	}
}

// The injection log said what was injected and never to whom, so the only way
// to tell whether a recall was used was the sentence the block asks the agent
// to say. In real-world stores, that sentence follows 22 of 1218
// injections — a measure of reporting, not of use. With the receiving session
// recorded, a reader can pair an injection with what the agent did next.
func TestSnapshotRecordsWhoReceivedIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigestInto(dir, KindDejaVu, "a block worth reading", "ses_1234", 2, 900,
		[]string{"pgbouncer"}, "claude:abc")

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(b))
	if line == "" {
		t.Fatal("nothing was written to the injection log")
	}
	var got Snapshot
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got.Into != "ses_1234" {
		t.Errorf("into = %q, want the agent session that received the block", got.Into)
	}
	if got.Kind != KindDejaVu || got.Sessions != 2 {
		t.Errorf("the rest of the record changed: %+v", got)
	}
}

// A caller that does not know the session still writes a usable record, and the
// field is omitted rather than written empty — older readers see what they saw.
func TestSnapshotWithoutAReceiverOmitsTheField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigestTerms(dir, KindDejaVu, "a block", 1, 100, nil)

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\"into\"") {
		t.Errorf("empty receiver was written out: %s", strings.TrimSpace(string(b)))
	}
}

// The déjà-vu hook recorded the receiver and the session-start hook did not,
// though it holds the id and uses it two lines away — so the same field meant
// two different things depending on which path wrote it (#1949).
func TestASessionStartDigestRecordsWhoItWentTo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)

	RecordDigestPolicyInto(dir, KindHook, "a digest of earlier work", "agent-session-1", 2, 4000, "local-only")

	got := Snapshots(dir, 0)
	if len(got) != 1 {
		t.Fatalf("wrote one snapshot, read %d", len(got))
	}
	if got[0].Into != "agent-session-1" {
		t.Errorf("the snapshot does not say where it went: %#v", got[0])
	}
	// And it still records the counting event, as the policy form always has.
	if tot := Totals(dir); tot.Injections != 1 || tot.Bytes == 0 {
		t.Errorf("the counting half was lost: %#v", tot)
	}
}

// The old form still exists and still writes no destination, so a caller that
// genuinely does not know one is not made to invent it. A harness that sends no
// session_id reaches the same place through the new form: the field is omitted,
// not filled with a placeholder.
func TestADigestWithNoKnownDestinationSaysNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)

	RecordDigestPolicy(dir, KindHook, "a digest of earlier work", 2, 4000, "local-only")
	RecordDigestPolicyInto(dir, KindHook, "another digest", "", 2, 4000, "local-only")

	got := Snapshots(dir, 0)
	if len(got) != 2 {
		t.Fatalf("wrote two snapshots, read %d", len(got))
	}
	for _, s := range got {
		if s.Into != "" {
			t.Errorf("a caller with no destination recorded one anyway: %q", s.Into)
		}
	}
}

// oneEvent writes a log holding a single event of the given kind.
func oneEvent(t *testing.T, kind string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	e := Event{
		Time: time.Now().UTC().Add(-time.Hour), Kind: kind,
		Bytes: 100, Sessions: 1, RawBytes: 1000, SessionIDs: []string{"s1"},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// What deja served an agent is one question, and the two screens that answer it
// walked the log with different rules: a blame was a recall to `deja stats` and
// nothing to `deja stats --impact`, so its bytes were missing from the ratio the
// impact screen prints (#1907). #1569 aligned five counters here; Impact was not
// among them.
func TestTheTwoScreensCountTheSameServings(t *testing.T) {
	for _, kind := range []string{KindRecall, KindContext, KindBlame, KindResource} {
		dir := oneEvent(t, kind)
		tot := Totals(dir)
		imp := Impact(dir)
		if tot.Recalls != 1 {
			t.Errorf("%s: stats counts %d recalls, want 1", kind, tot.Recalls)
		}
		if imp.Recalls != 1 {
			t.Errorf("%s: impact counts %d recalls, want 1", kind, imp.Recalls)
		}
		if imp.ServedBytes != 100 || imp.RawBytes != 1000 {
			t.Errorf("%s: impact served %d of %d raw, want 100 of 1000 — the distilled ratio is short by it",
				kind, imp.ServedBytes, imp.RawBytes)
		}
	}
}

// A read of deja://session/… hands the agent a whole session, which is why the
// kind exists — and nothing counted it at all.
func TestAResourceReadIsCountedAsServed(t *testing.T) {
	dir := oneEvent(t, KindResource)
	if tot := Totals(dir); tot.Recalls != 1 || tot.Bytes != 100 {
		t.Errorf("stats counts a resource read as %d recalls, %d bytes", tot.Recalls, tot.Bytes)
	}
}

// The kinds that write rather than serve stay uncounted on both screens.
func TestWritingKindsAreNotServings(t *testing.T) {
	for _, kind := range []string{KindRemember, KindSearch, KindHandoff} {
		dir := oneEvent(t, kind)
		if tot := Totals(dir); tot.Recalls != 0 || tot.Injections != 0 {
			t.Errorf("%s: stats counts it as served: %#v", kind, tot)
		}
		if imp := Impact(dir); imp.Recalls != 0 || imp.Injections != 0 {
			t.Errorf("%s: impact counts it as served: %#v", kind, imp)
		}
	}
}

// A write is never refused for want of the lock: every way of failing to take
// it ends in the write happening anyway (#2413).
func TestTheLogLockNeverRefusesAWrite(t *testing.T) {
	dir := t.TempDir()

	t.Run("a lock left by a dead process is taken over", func(t *testing.T) {
		p := filepath.Join(dir, "stale.jsonl")
		lock := p + ".lock"
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-logLockStale - time.Second)
		if err := os.Chtimes(lock, old, old); err != nil {
			t.Fatal(err)
		}
		ran := false
		withLogLock(p, func() { ran = true })
		if !ran {
			t.Error("a stale lock stopped the write")
		}
		if _, err := os.Stat(lock); err == nil {
			t.Error("the lock outlived the write that took it over")
		}
	})

	t.Run("a lock somebody is holding does not stop the write", func(t *testing.T) {
		p := filepath.Join(dir, "held.jsonl")
		lock := p + ".lock"
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(lock) }()
		start := time.Now()
		ran := false
		withLogLock(p, func() { ran = true })
		if !ran {
			t.Error("the write was dropped rather than raced")
		}
		if waited := time.Since(start); waited < logLockPoll {
			t.Errorf("it did not wait at all: %s", waited)
		}
		if _, err := os.Stat(lock); err != nil {
			t.Error("the writer removed a lock it never took")
		}
	})

	t.Run("a directory that cannot hold a lock", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes anywhere")
		}
		ro := filepath.Join(dir, "readonly")
		if err := os.MkdirAll(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		ran := false
		withLogLock(filepath.Join(ro, "log.jsonl"), func() { ran = true })
		if !ran {
			t.Error("a directory deja cannot lock stopped the write")
		}
	})
}

func topLevelJSONKeys(v any) map[string]bool {
	keys := map[string]bool{}
	rt := reflect.TypeOf(v)
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

func assertShape(t *testing.T, name string, got, want map[string]bool) {
	t.Helper()
	for k := range got {
		if !want[k] {
			t.Errorf("%s gained field %q — a change to a documented-stable --json shape", name, k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s lost field %q — a breaking change to the --json contract", name, k)
		}
	}
}

// `deja log --json` returns []Event and `deja log --last --json` returns one
// Snapshot. docs/json-output.md promises both shapes stay stable and only grow
// additively; these snapshots enforce that.
func TestLogJSONShapesAreStable(t *testing.T) {
	// "into" on the event is additive and optional, for the same reason it is
	// on the snapshot: the list showed several injections as identical rows,
	// and the recipient was one file over (#2307).
	assertShape(t, "Event", topLevelJSONKeys(Event{}), map[string]bool{
		"t": true, "kind": true, "bytes": true, "sessions": true,
		"empty": true, "raw": true, "ids": true, "into": true,
		// "unreadable" is additive and optional: an injection whose host sent
		// a payload deja could not decode has no receiver to name, and without
		// this its row is identical to one from a host that sent nothing
		// (#2161).
		"unreadable": true,
		// Compaction fields are additive and optional. They join a captured
		// raw transcript baseline to the first later edit without putting a
		// second state store beside the existing usage log.
		"compaction_session": true, "compaction_workspace": true,
		"compaction_revision": true, "tool_calls": true,
		"actions_before_edit": true, "measured": true,
		"compaction_error": true,
		"compaction_stage": true, "compaction_os_error": true,
		"compaction_source": true, "compaction_before_size": true,
		"compaction_after_size": true, "compaction_mtime_delta_ms": true,
		"compaction_read_ms": true, "compaction_same_file": true,
	})
	// "into" is additive and optional: it names the agent session an injection
	// went to, which the log never recorded. Without it the only measure of
	// whether a recall was used is the sentence the block asks the agent to
	// say — 22 of 1218 injections on a real store, which counts reporting.
	// "projects" is additive and optional: the trust policy answers for a
	// project name with no index at all, so a record that names its own
	// projects can be held to a rule tightened after it was written (#2324).
	assertShape(t, "Snapshot", topLevelJSONKeys(Snapshot{}), map[string]bool{
		"t": true, "kind": true, "sessions": true, "bytes": true,
		"policy": true, "terms": true, "into": true, "projects": true, "digest": true,
		"unreadable": true,
	})
}

// One injection writes an event and a digest snapshot. They carried separate
// time.Now() calls, so the two logs disagreed by microseconds about when the
// same thing happened and nothing could join them (#2294).
func TestAnInjectionCarriesOneStampInBothLogs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")

	RecordDigestPolicyInto(dir, "dejavu", "the digest text", "agent-1", 1, 63, "local+imported")

	events := Events(dir, 0)
	snaps := Snapshots(dir, 0)
	// The premise: exactly one of each was written.
	if len(events) != 1 || len(snaps) != 1 {
		t.Fatalf("%d events and %d snapshots, want one of each", len(events), len(snaps))
	}
	if !events[0].Time.Equal(snaps[0].Time) {
		t.Errorf("the same injection is stamped %s in the event log and %s in the snapshot log",
			events[0].Time.Format("15:04:05.000000"), snaps[0].Time.Format("15:04:05.000000"))
	}

	// The other pair writer has the same shape.
	dir2 := filepath.Join(t.TempDir(), "index.db")
	RecordDigestInto(dir2, "recall", "another digest", "agent-2", 2, 99, []string{"gateway"}, "s1", "s2")
	events, snaps = Events(dir2, 0), Snapshots(dir2, 0)
	if len(events) != 1 || len(snaps) != 1 {
		t.Fatalf("%d events and %d snapshots, want one of each", len(events), len(snaps))
	}
	if !events[0].Time.Equal(snaps[0].Time) {
		t.Errorf("RecordDigestInto stamps %s and %s",
			events[0].Time.Format("15:04:05.000000"), snaps[0].Time.Format("15:04:05.000000"))
	}

	// And the served-answer pair the MCP surfaces use, which called the two
	// writers back to back at the call site.
	dir3 := filepath.Join(t.TempDir(), "index.db")
	RecordServedSnapshot(dir3, "recall", "served text", 1, 42, []string{"s1"}, "local+imported")
	events, snaps = Events(dir3, 0), Snapshots(dir3, 0)
	if len(events) != 1 || len(snaps) != 1 {
		t.Fatalf("%d events and %d snapshots, want one of each", len(events), len(snaps))
	}
	if !events[0].Time.Equal(snaps[0].Time) {
		t.Errorf("RecordServedSnapshot stamps %s and %s",
			events[0].Time.Format("15:04:05.000000"), snaps[0].Time.Format("15:04:05.000000"))
	}
	if events[0].Bytes != len("served text") || snaps[0].Bytes != len("served text") {
		t.Errorf("the pair disagrees about size: %d and %d", events[0].Bytes, snaps[0].Bytes)
	}
}

// A stored digest outlives the rule it was served under, and the only way to
// hold it to a later one was recognising project names inside its own prose —
// which fails as soon as those sessions leave the index. The record names its
// own projects now (#2324).
func TestASnapshotNamesTheProjectsItWasBuiltFrom(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordServedFrom(dir, KindRecall, "a digest of two projects", 2, 900,
		[]string{"claude:abc", "claude:def"},
		[]string{"work/app", "imported:secretclient/api"}, "local+imported")

	got := lastSnapshot(t, dir)
	if len(got.Projects) != 2 || got.Projects[0] != "work/app" || got.Projects[1] != "imported:secretclient/api" {
		t.Errorf("projects = %v, want both, in the order they were served", got.Projects)
	}
	if got.Kind != KindRecall || got.Sessions != 2 || got.Policy != "local+imported" {
		t.Errorf("the rest of the record changed: %+v", got)
	}
	// The paired event is written by the same call, stamped alike.
	events := Events(dir, 0)
	if len(events) != 1 || !events[0].Time.Equal(got.Time) {
		t.Errorf("events = %+v, want one stamped like the digest at %s", events, got.Time)
	}
}

// The déjà-vu hook knows the receiving session, the terms and the projects.
func TestADejaVuDigestRecordsTermsAndProjects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigestFrom(dir, KindDejaVu, "a block worth reading", "ses_1234", 1, 400,
		[]string{"quaxbolt"}, []string{"work/app"}, []string{"claude:abc"})

	got := lastSnapshot(t, dir)
	if got.Into != "ses_1234" || len(got.Terms) != 1 || len(got.Projects) != 1 {
		t.Errorf("record = %+v, want the receiver, the terms and the project", got)
	}
	if events := Events(dir, 0); len(events) != 1 || events[0].Into != "ses_1234" {
		t.Errorf("events = %+v, want the receiver on the event too", events)
	}
}

// handoff writes a digest of one session under a named policy and no receiver.
func TestAPolicyDigestWithProjectsOmitsWhatItDoesNotKnow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigestPolicyFrom(dir, KindHandoff, "a handoff", "", 1, 100,
		[]string{"work/app"}, "local-only")

	got := lastSnapshot(t, dir)
	if got.Into != "" || got.Policy != "local-only" || len(got.Projects) != 1 {
		t.Errorf("record = %+v", got)
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"into"`) || strings.Contains(string(b), `"terms"`) {
		t.Errorf("empty fields were written out: %s", strings.TrimSpace(string(b)))
	}
}

// A writer that knows no projects leaves the field out, so a reader sees what
// it saw before the field existed.
func TestASnapshotWithoutProjectsOmitsTheField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordServedSnapshot(dir, KindRecall, "a digest", 1, 100, []string{"claude:abc"}, "")

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"projects"`) {
		t.Errorf("empty projects were written out: %s", strings.TrimSpace(string(b)))
	}
}

func lastSnapshot(t *testing.T, dir string) Snapshot {
	t.Helper()
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var got Snapshot
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// forget takes the messages and the notes' borrowed titles; the digests served
// from those sessions stayed, so a page republished forgotten text (#2325).
func TestForgetSnapshotsDropsWhatItIsToldAndNothingElse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordServedFrom(dir, KindRecall, "a peer digest", 1, 100, nil, []string{"imported:client/api"}, "")
	RecordServedFrom(dir, KindRecall, "my own digest", 1, 100, nil, []string{"work/app"}, "")

	match := func(s Snapshot) bool {
		return slices.Contains(s.Projects, "imported:client/api")
	}
	if n := CountSnapshots(dir, match); n != 1 {
		t.Fatalf("CountSnapshots = %d, want the one record that matches", n)
	}
	gone, err := ForgetSnapshots(dir, match)
	if err != nil {
		t.Fatal(err)
	}
	if gone != 1 {
		t.Errorf("dropped %d records, want 1", gone)
	}
	left := Snapshots(dir, 0)
	if len(left) != 1 || left[0].Digest != "my own digest" {
		t.Errorf("what is left = %+v, want the other project's digest", left)
	}
	// The events stay: they say something was served, not what it said.
	if events := Events(dir, 0); len(events) != 2 {
		t.Errorf("events = %d, want both", len(events))
	}
	// A sweep that matches nothing rewrites nothing.
	if n, err := ForgetSnapshots(dir, match); err != nil || n != 0 {
		t.Errorf("second sweep dropped %d (err %v), want 0", n, err)
	}
}

// An empty log is not an error, and there is nothing to count in it.
func TestForgetSnapshotsOnAnEmptyLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if n, err := ForgetSnapshots(dir, func(Snapshot) bool { return true }); err != nil || n != 0 {
		t.Errorf("ForgetSnapshots on an empty log = %d, %v", n, err)
	}
	if n := CountSnapshots(dir, func(Snapshot) bool { return true }); n != 0 {
		t.Errorf("CountSnapshots on an empty log = %d", n)
	}
}

// `deja stats` prints "Recalls served" and "Injections" as two lines, and the
// first used to include the second: a log of two agent recalls and three
// session-start injections reported 5 and 3, while `deja stats --impact`
// reported 2 for the same events.
func TestTotalsCountsRecallsApartFromInjections(t *testing.T) {
	dir := usageDir(t)
	now := time.Now()
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: now, Bytes: 100, Sessions: 1})
	appendEventForTest(t, dir, Event{Kind: KindContext, Time: now, Bytes: 100, Sessions: 1})
	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 300, Sessions: 3})
	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 300, Sessions: 3})
	appendEventForTest(t, dir, Event{Kind: KindDejaVu, Time: now, Bytes: 300, Sessions: 3})

	got := Totals(dir)
	if got.Recalls != 2 {
		t.Errorf("recalls served = %d, want 2 — the three injections are counted on their own line", got.Recalls)
	}
	if got.Injections != 3 {
		t.Errorf("injections = %d, want 3", got.Injections)
	}
	if got.Recalls+got.Injections != 5 {
		t.Errorf("recalls+injections = %d, want the 5 logged events", got.Recalls+got.Injections)
	}
}

// The empty-result rate is a share of the agent recalls that returned nothing.
// Its denominator used to be Recalls minus Injections, which only worked while
// Recalls carried both.
func TestEmptyResultRateIsOverRecallsOnly(t *testing.T) {
	dir := usageDir(t)
	now := time.Now()
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: now, Bytes: 100, Sessions: 1})
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: now, Empty: true})
	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 300, Sessions: 3})

	if got := Totals(dir).EmptyResultRate; got != 0.5 {
		t.Errorf("empty result rate = %v, want 0.5 (1 of 2 recalls)", got)
	}
}

// `RecordSize` says a record is three times its answer at worst. Two of those
// bytes are the newlines and quotes a digest is full of; the third is a byte
// that is not valid UTF-8, written as the replacement character, which nothing
// strips. Two classes are kept out of the multiplier instead of allowed for:
// control bytes, which SafeText removes, and the angle brackets and ampersands
// encoding/json would escape to six bytes each — the writer turns that escaping
// off, because they are ordinary text (#1982).
func TestWhatARecordWeighs(t *testing.T) {
	const budget = 8192
	for _, c := range []struct {
		name   string
		body   string
		within bool
	}{
		{"plain text", strings.Repeat("the pgbouncer pool kept timing out ", 234), true},
		{"a digest full of newlines", strings.Repeat("- session `abc`\n  user: something happened\n", 195), true},
		{"every byte a newline", strings.Repeat("\n", budget), true},
		{"every byte a quote", strings.Repeat(`"`, budget), true},
		{"cyrillic", strings.Repeat("отладка ", 1024), true},
		{"angle brackets, as code has them", strings.Repeat("<T> & Vec<U> ", 630), true},
		{"a shell pipeline", strings.Repeat("cat a.txt | grep x > out.txt && ", 256), true},
		// One run of invalid bytes is one replacement character — ToValidUTF8
		// collapses it — so a solid block measures nothing. What costs is an
		// invalid byte beside a character JSON escapes: three out for the one,
		// two for the other, and 2.5 is the ceiling that gives.
		{"invalid bytes in one run", strings.Repeat("\xff", budget), true},
		// A plain letter costs one byte out, so this is 2x — the same as a
		// newline on its own, and here to show what the neighbour has to be.
		{"invalid bytes alternating with text", strings.Repeat("\xffa", budget/2), true},
		{"invalid bytes alternating with newlines", strings.Repeat("\xff\n", budget/2), true},
		// The one class the bound does not cover, kept so the reason is on the
		// record: six bytes each, and RecordSize would be wrong by twice.
		{"control bytes, which cannot reach here", strings.Repeat("\x01", budget), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := marshalSnapshot(Snapshot{
				Time: time.Now().UTC(), Kind: KindBlame, Sessions: 3,
				Bytes: len(c.body), Policy: "local+imported",
				Terms: []string{"pgbouncer", "pool"}, Into: "agent-session-1",
				Digest: c.body,
			})
			if err != nil {
				t.Fatal(err)
			}
			within := len(b) <= RecordSize(len(c.body))
			if within != c.within {
				t.Errorf("record is %d bytes for a body of %d; RecordSize says %d",
					len(b), len(c.body), RecordSize(len(c.body)))
			}
		})
	}
}

// And the room holds the largest answer deja serves, at that bound.
func TestTheLargestAnswerFitsTheRoomWithEscaping(t *testing.T) {
	const largest = 8192 // blame and recall_context, the widest budgets today
	if got := RecordSize(largest); got > RecordRoom {
		t.Errorf("the largest answer weighs %d against %d of room", got, RecordRoom)
	}
	// Not by so much that the check is vacuous: an answer twice as wide would
	// not fit, which is the change this is here to catch.
	if RecordSize(2*largest) <= RecordRoom {
		t.Errorf("a budget of %d would still pass, so this bound constrains nothing", 2*largest)
	}
}

// A record's size must not depend on which Go release built deja. An invalid
// byte is written as the replacement character, and 1.25 escapes that as six
// bytes where 1.27 writes it raw as three — so the digest is coerced to valid
// UTF-8 before it is marshalled, and the answer is three either way (#1982).
func TestARecordIsTheSameSizeOnEveryToolchain(t *testing.T) {
	body := strings.Repeat("\xff", 8192)
	b, err := marshalSnapshot(Snapshot{
		Time: time.Now().UTC(), Kind: KindBlame, Sessions: 1,
		Bytes: len(body), Digest: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `\ufffd`) {
		t.Errorf("the record carries escaped replacement characters, which cost six bytes each")
	}
	if len(b) > RecordSize(len(body)) {
		t.Errorf("record is %d bytes for a body of %d; the bound says %d", len(b), len(body), RecordSize(len(body)))
	}
}

// The log is rewritten past 1MB keeping the last 14 days, so a count with no
// period attached reads as a lifetime total and then falls by orders of
// magnitude when that happens (#763).
func TestTotalsReportsTheOldestEventItCounted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := Path(dir)
	old := time.Now().UTC().AddDate(0, 0, -30)
	mid := time.Now().UTC().AddDate(0, 0, -3)
	var lines []byte
	for _, at := range []time.Time{mid, old, mid} {
		b, err := json.Marshal(Event{Time: at, Kind: KindRecall, Bytes: 100, Sessions: 1})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, append(b, '\n')...)
	}
	if err := os.WriteFile(p, lines, 0o600); err != nil {
		t.Fatal(err)
	}
	got := Totals(dir)
	if got.Recalls != 3 {
		t.Fatalf("recalls = %d", got.Recalls)
	}
	// The oldest, not the first line: a log is appended to, but rotation and
	// clock changes can leave it out of order.
	if !got.Since.Equal(old) {
		t.Errorf("since = %v, want %v", got.Since, old)
	}

	// An empty log has no period to report.
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Totals(dir); !got.Since.IsZero() || got.Recalls != 0 {
		t.Errorf("empty log: since=%v recalls=%d", got.Since, got.Recalls)
	}
}

func TestRecordDigestAndSnapshots(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigest(dir, KindHook, "first digest", 2, 1024)
	RecordDigest(dir, KindRecall, "second digest", 1, 1024)

	snaps := Snapshots(dir, 10)
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snaps))
	}
	if snaps[0].Digest != "second digest" || snaps[0].Kind != KindRecall || snaps[0].Sessions != 1 {
		t.Fatalf("newest snapshot = %+v", snaps[0])
	}
	if snaps[1].Digest != "first digest" {
		t.Fatalf("oldest snapshot = %+v", snaps[1])
	}

	events := Events(dir, 10)
	if len(events) != 2 || events[0].Kind != KindRecall || events[0].Bytes != len("second digest") {
		t.Fatalf("events = %+v", events)
	}
}

func TestRecordDigestEmptySkipsSnapshot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigest(dir, KindRecall, "", 0, 1024)
	if snaps := Snapshots(dir, 10); len(snaps) != 0 {
		t.Fatalf("empty digest recorded a snapshot: %+v", snaps)
	}
	events := Events(dir, 10)
	if len(events) != 1 || !events[0].Empty {
		t.Fatalf("empty event = %+v", events)
	}
}

func TestSnapshotRotationKeepsNewest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	big := strings.Repeat("x", 60<<10)
	for i := range 12 {
		RecordDigest(dir, KindHook, big+string(rune('a'+i)), 1, 2048)
	}
	snaps := Snapshots(dir, 0)
	if len(snaps) == 0 || len(snaps) > snapshotsToKeep+2 {
		t.Fatalf("rotation kept %d snapshots", len(snaps))
	}
	if !strings.HasSuffix(snaps[0].Digest, "l") {
		t.Fatalf("newest snapshot lost after rotation, tail = %q", snaps[0].Digest[len(snaps[0].Digest)-1:])
	}
}

func TestEventsLimitAndOrder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	for i := range 5 {
		RecordResult(dir, KindSearch, i, 0, false)
	}
	events := Events(dir, 3)
	if len(events) != 3 || events[0].Bytes != 4 || events[2].Bytes != 2 {
		t.Fatalf("events = %+v", events)
	}
}

func TestWornSessionsCountsAgentRecalls(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordServedSessions(dir, KindRecall, 100, 2, false, 5000, []string{"s1", "s2"})
	RecordServedSessions(dir, KindContext, 80, 1, false, 3000, []string{"s1"})
	RecordDigest(dir, KindHook, "hook digest", 3, 900) // hook pushes must not count
	worn := WornSessions(dir)
	if worn["s1"] != 2 || worn["s2"] != 1 {
		t.Fatalf("worn = %v", worn)
	}
	if len(worn) != 2 {
		t.Fatalf("hook injection leaked into worn: %v", worn)
	}
}

// One pass for the line that renders on every prompt. The numbers have to be
// the ones the separate readers gave, or this is a different status line
// (#2224).
func TestStatusCountersAgreeWithTheReadersTheyReplace(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Today, whatever the hour: "two hours ago" is yesterday between midnight
	// and two in the morning, and the counters this test is about cut at local
	// midnight — so the fixture would have had nothing in it and the test would
	// have said so at 00:24.
	today := func(ago time.Duration) time.Time {
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if at := now.Add(-ago); !at.Before(midnight) {
			return at
		}
		return midnight.Add(time.Second)
	}
	appendEventForTest(t, dir, Event{Time: today(2 * time.Hour), Kind: KindRecall, Bytes: 900, Sessions: 2, RawBytes: 9000})
	appendEventForTest(t, dir, Event{Time: today(3 * time.Hour), Kind: KindContext, Bytes: 400, Sessions: 1, RawBytes: 4000})
	appendEventForTest(t, dir, Event{Time: today(1 * time.Hour), Kind: KindHook, Bytes: 600, Sessions: 1, RawBytes: 6000})
	// An empty recall serves nothing and must not count, an empty injection
	// carried the environment block and its bytes still do (#1962).
	appendEventForTest(t, dir, Event{Time: today(30 * time.Minute), Kind: KindRecall, Empty: true})
	appendEventForTest(t, dir, Event{Time: today(40 * time.Minute), Kind: KindHook, Bytes: 300, Empty: true})
	// Earlier in the week but not today.
	appendEventForTest(t, dir, Event{Time: now.Add(-72 * time.Hour), Kind: KindRecall, Bytes: 100, Sessions: 1, RawBytes: 1000})
	// Older than the week.
	appendEventForTest(t, dir, Event{Time: now.Add(-20 * 24 * time.Hour), Kind: KindRecall, Bytes: 5000, Sessions: 1, RawBytes: 50000})

	got := StatusCounters(dir)
	recalls, bytes, injected := TodayDemand(dir)
	if got.Recalls != recalls || got.Bytes != bytes || got.Injected != injected {
		t.Errorf("today: one pass says %d/%d/%d, TodayDemand says %d/%d/%d",
			got.Recalls, got.Bytes, got.Injected, recalls, bytes, injected)
	}
	wr, wb, _, _ := Week(dir)
	if got.WeekRecalls != wr || got.WeekBytes != wb {
		t.Errorf("week: one pass says %d/%d, Week says %d/%d", got.WeekRecalls, got.WeekBytes, wr, wb)
	}
	if raw := TodayRaw(dir); got.RawToday != raw {
		t.Errorf("raw: one pass says %d, TodayRaw says %d", got.RawToday, raw)
	}
	if got.ServedRawToday != 13000 {
		t.Errorf("ServedRawToday = %d, want 13000 (served events only, excluding hook raw bytes)", got.ServedRawToday)
	}
	// The premise: these numbers are not all zero, or agreeing means nothing.
	if got.Recalls == 0 || got.Bytes == 0 || got.Injected == 0 || got.WeekRecalls == 0 || got.RawToday == 0 {
		t.Fatalf("the fixture produced %+v, so agreement measures nothing", got)
	}
}

// An empty log answers zeros rather than reading anything into them.
func TestStatusCountersOnAnEmptyLog(t *testing.T) {
	got := StatusCounters(t.TempDir())
	if got != (StatusNumbers{}) {
		t.Errorf("an empty log reports %+v", got)
	}
}

// RecordDigestTerms is RecordDigest plus the query terms, for déjà vu audits,
// and the ids of the sessions the moment surfaced.
//
// The ids were missing, which meant a déjà vu moment — the user returning to
// ground they had covered before — left no trace on the session it was about.
// It is the only signal deja has that the *user* came back, as opposed to an
// agent pulling something, and it could not reach ranking at all.
func RecordDigestTerms(indexDir, kind, digest string, sessions int, raw int64, terms []string, ids ...string) {
	RecordDigestInto(indexDir, kind, digest, "", sessions, raw, terms, ids...)
}

// SnapshotOnly stores the digest text without writing a counting event, for
// callers that already recorded one with extra fields.
func SnapshotOnly(indexDir, kind, digest string, sessions int) {
	snapshotWrite(indexDir, kind, digest, sessions, "")
}

// SnapshotPolicy is SnapshotOnly plus the policy name for the audit trail.
func SnapshotPolicy(indexDir, kind, digest string, sessions int, policyName string) {
	snapshotWrite(indexDir, kind, digest, sessions, policyName)
}

// RecordServedSnapshot writes the counting event and the digest snapshot for
// one served answer, stamped alike.
func RecordServedSnapshot(indexDir, kind, digest string, sessions int, raw int64, ids []string, policyName string) {
	RecordServedFrom(indexDir, kind, digest, sessions, raw, ids, nil, policyName)
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

// TodayRaw sums the source-transcript volume behind today's served digests.
func TodayRaw(indexDir string) int64 {
	return StatusCounters(indexDir).RawToday
}

// Today sums today's agent-memory events and their served bytes.
func Today(indexDir string) (recalls int, bytes int) {
	recalls, bytes, _ = TodayWithInjections(indexDir)
	return recalls, bytes
}

// A process killed between the record and its newline costs that record — the
// log is appended without a lock by design and `read` drops a line that does
// not parse. It cost one more: the next event was appended onto the partial
// line, so that line failed to parse too and the recall just served went
// missing from every count (#1901).
func TestARecordAfterATruncatedTailSurvives(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	good := `{"t":"2026-08-20T10:00:00Z","kind":"recall","bytes":100,"sessions":1}`
	partial := `{"t":"2026-08-21T10:00:00Z","kind":"recall","byte`
	if err := os.WriteFile(p, []byte(good+"\n"+partial), 0o600); err != nil {
		t.Fatal(err)
	}

	RecordResult(dir, KindRecall, 200, 1, false)

	tot := Totals(dir)
	if tot.Recalls != 2 {
		body, _ := os.ReadFile(p)
		t.Fatalf("counted %d recalls, want the old one and the new one:\n%s", tot.Recalls, body)
	}
	if tot.Bytes != 300 {
		t.Errorf("counted %d bytes, want 100 + 200", tot.Bytes)
	}
	// The partial record itself stays lost — that is the trade the log makes —
	// but it must not take a second line with it.
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"byte{`) {
		t.Errorf("the new record was appended onto the partial line:\n%s", body)
	}
}

// The ordinary case is unchanged: no stray blank line, no doubled newline.
func TestAnOrdinaryRecordAddsOneLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	RecordResult(dir, KindRecall, 100, 1, false)
	RecordResult(dir, KindRecall, 200, 1, false)
	body, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"); len(lines) != 2 {
		t.Errorf("two records wrote %d lines:\n%s", len(lines), body)
	}
	if strings.Contains(string(body), "\n\n") {
		t.Errorf("a blank line crept in:\n%s", body)
	}
}

// An injection whose host sent a payload deja could not decode is recorded
// either way — the memory did go out — and both writers carry the flag, so
// neither surface reads like a row from a host that sent nothing (#2161).
func TestAnUnreadablePayloadIsWrittenToBothLogs(t *testing.T) {
	dir := t.TempDir()
	RecordDigestPolicySessionsUnread(dir, KindHook, "a digest", "ses_half", 2, 4000, "local-only",
		[]string{"claude:one"}, []string{"beta"})

	events := read(Path(dir))
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	if !events[0].Unreadable {
		t.Errorf("the event does not say the payload could not be read: %+v", events[0])
	}
	// The receiver a half-decoded payload did name is kept: the flag is about
	// the payload, not about the id.
	if events[0].Into != "ses_half" {
		t.Errorf("the event dropped the receiver: %+v", events[0])
	}

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.Unreadable {
		t.Errorf("the snapshot does not say the payload could not be read:\n%s", b)
	}
	if snap.Into != "ses_half" || snap.Policy != "local-only" || len(snap.Projects) != 1 {
		t.Errorf("the snapshot lost a field on the way: %+v", snap)
	}
}

// The ordinary recorders leave the flag off, so an older row and a readable
// one read the same.
func TestAReadablePayloadIsNotFlagged(t *testing.T) {
	dir := t.TempDir()
	RecordDigestPolicySessionsFrom(dir, KindHook, "a digest", "ses1", 2, 4000, "local-only", nil, nil)
	events := read(Path(dir))
	if len(events) != 1 || events[0].Unreadable {
		t.Errorf("a readable payload was flagged: %+v", events)
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "unreadable") {
		t.Errorf("the snapshot carries the flag for a payload deja read:\n%s", b)
	}
}

// The déjà vu hook's own recorder: the prompt is what it recalls from, so a
// decode that fails on a later field still injects — and the row carries the
// receiver the payload did name beside the flag about the payload (#2773).
func TestTheDejaVuRecorderKeepsBothTheFlagAndTheReceiver(t *testing.T) {
	dir := t.TempDir()
	RecordDigestFromUnread(dir, KindDejaVu, "a digest", "ses_vu", 2, 4000,
		[]string{"pgbouncer"}, []string{"beta"}, []string{"claude:one"})

	events := read(Path(dir))
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	if !events[0].Unreadable || events[0].Into != "ses_vu" {
		t.Errorf("the event lost half of what it was told: %+v", events[0])
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.Unreadable || snap.Into != "ses_vu" || len(snap.Terms) != 1 {
		t.Errorf("the snapshot lost a field on the way: %+v", snap)
	}
}

// deja had two ways of saying "a week ago" and showed them side by side: the
// status bar's week counters cut at a fixed 168 hours while its déjà-vu count
// cut seven calendar days back. In a zone with daylight saving those differ by
// an hour for one week in each direction, so an event inside that hour was in
// one number and not the other (#1920). The day counters beside them use local
// midnight, which is calendar, and so does the brief.
func TestTheWeekIsSevenCalendarDays(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	for _, c := range []struct {
		what string
		now  time.Time
	}{
		{"after the autumn change", time.Date(2026, 11, 3, 12, 0, 0, 0, ny)},
		{"after the spring change", time.Date(2026, 3, 10, 12, 0, 0, 0, ny)},
		{"an ordinary week", time.Date(2026, 8, 25, 12, 0, 0, 0, ny)},
	} {
		cut := WeekCut(c.now)
		if got, want := cut.Format("2006-01-02 15:04"), c.now.AddDate(0, 0, -7).Format("2006-01-02 15:04"); got != want {
			t.Errorf("%s: week opens at %s, want the same wall time seven days back (%s)", c.what, got, want)
		}
		// The wall clock, not 168 hours: that is what makes it the same week
		// the day counters and the brief are talking about.
		if h, m := cut.Hour(), cut.Minute(); h != 12 || m != 0 {
			t.Errorf("%s: week opens at %02d:%02d, not the hour it is now", c.what, h, m)
		}
	}
}

// The hour the two rules disagreed about, asserted rather than skipped past.
// After the autumn change the fixed-hours cut lands an hour later than the
// calendar one, so an event in between is inside this week by the rule deja
// keeps and outside it by the rule it dropped — which is the event that used to
// be in one counter and not the other.
func TestTheHourTheOldRuleWouldHaveMissed(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, ny)
	fixed := now.Add(-7 * 24 * time.Hour)
	cut := WeekCut(now)
	if !cut.Before(fixed) {
		t.Fatalf("this week has no clock change in it: cut=%s fixed=%s", cut, fixed)
	}
	event := cut.Add(30 * time.Minute)
	if event.Before(cut) {
		t.Errorf("an event half an hour into the week is outside it")
	}
	if !event.Before(fixed) {
		t.Errorf("the event is not in the hour the two rules disagreed about: %s", event)
	}
}

// And in spring the disagreement runs the other way: the fixed-hours cut opens
// an hour early, so it counted an event from the week before.
func TestTheHourTheOldRuleWouldHaveAdded(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, ny)
	fixed := now.Add(-7 * 24 * time.Hour)
	cut := WeekCut(now)
	if !fixed.Before(cut) {
		t.Fatalf("this week has no clock change in it: cut=%s fixed=%s", cut, fixed)
	}
	event := fixed.Add(30 * time.Minute)
	if !event.Before(cut) {
		t.Errorf("an event before this week's start is counted inside it: %s", event)
	}
}
