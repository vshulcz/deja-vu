package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agedLog writes a log past the rotation size whose events are all the given
// number of days old.
func agedLog(t *testing.T, days int) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	path = Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var b strings.Builder
	for i := range 4000 {
		e := Event{
			Time: now.Add(-time.Duration(days+i%5) * 24 * time.Hour), Kind: KindRecall,
			Bytes: 500, Sessions: 2, RawBytes: 5000, SessionIDs: []string{strings.Repeat("x", 200)},
		}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// Rotation drops what is older than the keep window, and when everything is
// older than it — a fortnight away from the machine — that used to be the whole
// file. The first recall on return left zero bytes, and `deja stats --impact`
// said "no recall activity recorded yet" about a log that had held four
// thousand events (#1922). Bounding the file is the job; erasing the record is
// not.
func TestRotationLeavesTheNewestEventsWhenAllAreOld(t *testing.T) {
	dir, p := agedLog(t, 21)
	before := len(read(p))
	if before == 0 {
		t.Fatal("the fixture wrote nothing")
	}

	rotate(p)

	kept := read(p)
	if len(kept) == 0 {
		t.Fatalf("rotation left nothing of %d events", before)
	}
	if len(kept) >= before {
		t.Errorf("rotation kept %d of %d — it is meant to bound the file", len(kept), before)
	}
	if tot := Totals(dir); tot.Since.IsZero() {
		t.Error("the window says the log holds nothing, though it holds the newest events")
	}
	if imp := Impact(dir); imp.Recalls == 0 {
		t.Error("the impact screen reports no activity after a rotation that kept events")
	}
	// The newest ones, not an arbitrary slice: the last thing that happened is
	// what a reader wants when the rest has aged out.
	newest := kept[0].Time
	for _, e := range kept {
		if e.Time.After(newest) {
			newest = e.Time
		}
	}
	if age := time.Since(newest).Hours() / 24; age > 22 {
		t.Errorf("the newest kept event is %.0f days old, so the oldest were kept instead", age)
	}
}

// The ordinary case is unchanged: with events inside the window, only those
// stay.
func TestRotationStillDropsWhatIsOutsideTheWindow(t *testing.T) {
	dir, p := agedLog(t, 0)
	// Half of them well outside the window.
	old := Event{Time: time.Now().UTC().Add(-40 * 24 * time.Hour), Kind: KindRecall, Bytes: 1, Sessions: 1}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	rotate(p)

	for _, e := range read(p) {
		if time.Since(e.Time) > 15*24*time.Hour {
			t.Errorf("an event %s old survived a rotation with fresh events in the file", time.Since(e.Time))
			break
		}
	}
	if tot := Totals(dir); tot.Recalls == 0 {
		t.Error("the fresh events were dropped too")
	}
}

// A log over the size trigger because it is busy rather than old drops
// nothing, and #1900 stopped rewriting it for that reason. The read that
// decides it stayed: every event paid for the whole file, 6.4 ms against 28 µs
// on a fresh log and climbing (#1972). What one read found now answers for the
// next, so a process that records more than once pays it once.
//
// A ratio rather than a duration, so it means the same thing on a slow runner.
//
// Best of three on each side, because one loop is about six milliseconds and a
// runner busy with the rest of the suite stretches that by twenty: measured,
// one sample of each swung between 0.09 and 2.25 while the machine was loaded,
// and this test has failed once that way and passed alone. The minimum of
// several runs is the sample that was not interrupted.
func TestAWriteDoesNotPayForTheWholeLog(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	best := func(prepare func(dir string)) time.Duration {
		fastest := time.Duration(1<<62 - 1)
		for range 3 {
			dir := t.TempDir()
			prepare(dir)
			start := time.Now()
			for range 200 {
				RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
			}
			if took := time.Since(start); took < fastest {
				fastest = took
			}
		}
		return fastest
	}
	base := best(func(string) {})
	full := best(func(dir string) {
		for range 12000 {
			RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
		}
	})

	// Both sides have to be worth measuring, or the ratio below is one piece
	// of noise divided by another.
	// Low enough that a machine several times faster than this one still
	// clears it — two hundred writes take about 6 ms here — and high enough
	// that a sample of pure noise does not become a ratio.
	if base < 200*time.Microsecond {
		t.Fatalf("two hundred writes to a fresh log took %v, which is too cheap to compare against", base)
	}
	// Ten times is far below the 135× this same test measures on the branch
	// before the fix, and far above the noise between two runs of one loop.
	if full > base*10 {
		t.Errorf("writing to a full log took %v against %v on a fresh one", full, base)
	}
	t.Logf("fresh %v, full %v", base, full)
}

// The memo says what one read found, and it has to stop saying it. An event
// stamped in the past — a clock that stepped back, a log carried from another
// machine — arrives behind the memo, and without an expiry it would wait out
// the whole fourteen-day window before anything looked again (#1972).
func TestABackdatedEventIsStillDropped(t *testing.T) {
	dir := t.TempDir()
	for range 12000 {
		RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	}
	// The memo is in hand now: the log is over the threshold with nothing to
	// drop, so the read that just happened answers for the next few minutes.
	old, err := json.Marshal(Event{Time: time.Now().UTC().Add(-100 * 24 * time.Hour), Kind: KindRecall, Bytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(Path(dir), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(old, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	nothingToDrop.Delete(Path(dir)) // stand in for the memo expiring
	RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)

	for _, e := range read(Path(dir)) {
		if e.Time.Before(time.Now().UTC().Add(-keepWindow)) {
			t.Errorf("an event %v old survived the rotation", time.Since(e.Time).Round(time.Hour))
			break
		}
	}
}

// And the memo does not outlive the file it describes: a rotation by another
// process shrinks it, and what this process remembers is about the file that
// was replaced.
func TestAShrunkLogIsReadAgain(t *testing.T) {
	dir := t.TempDir()
	for range 12000 {
		RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	}
	before, ok := nothingToDrop.Load(Path(dir))
	if !ok {
		t.Fatal("no memo was stored for a log with nothing to drop")
	}
	m, ok := before.(rotationMemo)
	if !ok {
		t.Fatalf("memo is a %T", before)
	}
	if skipRotation(Path(dir), m.size-1) {
		t.Error("a log that shrank under this process was taken on trust")
	}
	if !skipRotation(Path(dir), m.size) {
		t.Error("a log that only grew was read again for nothing")
	}
}

// A memo older than its window is not used, whatever it says.
func TestAStaleMemoIsNotUsed(t *testing.T) {
	dir := t.TempDir()
	p := Path(dir)
	nothingToDrop.Store(p, rotationMemo{
		oldest: time.Now().UTC(),
		size:   0,
		at:     time.Now().UTC().Add(-memoWindow - time.Minute),
	})
	if skipRotation(p, 1<<20) {
		t.Error("a memo older than its window was still trusted")
	}
}

// The counters still answer after all this, which is what the log is for.
func TestTheLogStillAnswersAfterABusyFortnight(t *testing.T) {
	dir := t.TempDir()
	for range 2000 {
		RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	}
	recalls, bytes, _, _ := Week(dir)
	if recalls == 0 || bytes == 0 {
		t.Errorf("the week reads %d recalls and %d bytes", recalls, bytes)
	}
	if !strings.Contains(Path(dir), ".usage.jsonl") {
		t.Errorf("the log moved: %s", Path(dir))
	}
}

// A log over the rotation trigger with nothing old enough to drop is the
// ordinary state of a busy machine, and every short-lived hook used to read and
// parse the whole file to find that out — 13 ms on 12 000 events, against 64 µs
// for the same write once a memo exists. The answer is one bit and the stamps
// are text (#2220).
func TestDecidingThereIsNothingToDropDoesNotParseTheLog(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	write := func(dir string, n int, oldest time.Duration) {
		t.Helper()
		var b strings.Builder
		now := time.Now().UTC()
		step := oldest / time.Duration(n)
		for i := range n {
			stamp := now.Add(-time.Duration(i) * step).Format(time.RFC3339Nano)
			fmt.Fprintf(&b, `{"t":%q,"kind":"recall","bytes":900,"sessions":2,"raw":9000,"ids":["s%d"]}`+"\n", stamp, i)
		}
		if err := os.WriteFile(Path(dir), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The premise: this log is over the trigger, so rotate does not simply
	// stat it and leave.
	dir := t.TempDir()
	write(dir, 12000, 13*24*time.Hour)
	fi, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < rotateAt {
		t.Fatalf("the fixture is %d bytes, under the %d trigger, so this measures nothing", fi.Size(), rotateAt)
	}

	// Two cold writes of the same size, best of three each: one over a log with
	// nothing to drop, one over a log that has to be rotated. The second is the
	// work the first used to do. Comparing them rather than the clock keeps the
	// measurement about the parse and not about the machine.
	best := func(age time.Duration) time.Duration {
		t.Helper()
		fastest := time.Duration(1<<62 - 1)
		for range 3 {
			fresh := t.TempDir()
			write(fresh, 12000, age)
			start := time.Now()
			RecordResultRaw(fresh, KindHook, 600, 1, false, 6000)
			if took := time.Since(start); took < fastest {
				fastest = took
			}
		}
		return fastest
	}
	nothingOld := best(13 * 24 * time.Hour)
	mustRotate := best(30 * 24 * time.Hour)
	warm := time.Duration(1<<62 - 1)
	for range 3 {
		start := time.Now()
		RecordResultRaw(dir, KindHook, 600, 1, false, 6000)
		if took := time.Since(start); took < warm {
			warm = took
		}
	}
	t.Logf("cold with nothing to drop %v, cold with a rotation %v, warm %v",
		nothingOld.Round(time.Microsecond), mustRotate.Round(time.Microsecond), warm.Round(time.Microsecond))
	// A third of the rotation's cost. Before this the two were the same call
	// and measured within noise of each other; parsing is what separates them.
	if nothingOld > mustRotate/3 {
		t.Errorf("deciding there was nothing to drop cost %v against %v to actually rotate: it is still parsing the whole log",
			nothingOld, mustRotate)
	}

	// And the rotation it decides about still happens when it should.
	old := t.TempDir()
	write(old, 12000, 30*24*time.Hour)
	before, err := os.Stat(Path(old))
	if err != nil {
		t.Fatal(err)
	}
	RecordResultRaw(old, KindHook, 600, 1, false, 6000)
	after, err := os.Stat(Path(old))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Errorf("a log holding a month of events was not rotated: %d bytes, was %d", after.Size(), before.Size())
	}
	for _, e := range read(Path(old)) {
		if time.Since(e.Time) > keepWindow+time.Hour {
			t.Errorf("an event %v old survived the rotation", time.Since(e.Time).Round(time.Hour))
			break
		}
	}
}

// The scan answers with what the parser would have answered, on the shapes a
// log actually carries: a fraction that RFC3339Nano trimmed, a stamp with none
// at all, an event out of order behind a newer one, and lines this scan cannot
// speak for — a foreign field order, a truncated tail — which have to fall back
// rather than answer (#2220).
func TestTheStampScanAgreesWithTheParser(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, c := range []struct {
		name  string
		lines []string
		ok    bool
		want  time.Time
	}{
		{
			name: "trimmed fractions in one second",
			lines: []string{
				`{"t":"` + now.Add(500*time.Millisecond).Format(time.RFC3339Nano) + `","kind":"recall","bytes":1}`,
				`{"t":"` + now.Format(time.RFC3339Nano) + `","kind":"recall","bytes":1}`,
				`{"t":"` + now.Add(50*time.Millisecond).Format(time.RFC3339Nano) + `","kind":"recall","bytes":1}`,
			},
			ok:   true,
			want: now,
		},
		{
			name: "an older event behind a newer one",
			lines: []string{
				`{"t":"` + now.Format(time.RFC3339Nano) + `","kind":"recall","bytes":1}`,
				`{"t":"` + now.Add(-72*time.Hour).Format(time.RFC3339Nano) + `","kind":"recall","bytes":1}`,
			},
			ok:   true,
			want: now.Add(-72 * time.Hour),
		},
		{
			name:  "another field first",
			lines: []string{`{"kind":"recall","t":"` + now.Format(time.RFC3339Nano) + `","bytes":1}`},
			ok:    false,
		},
		{
			name:  "a stamp with an offset rather than Z",
			lines: []string{`{"t":"2026-08-27T10:00:00+02:00","kind":"recall","bytes":1}`},
			ok:    false,
		},
		{
			name:  "a truncated last line",
			lines: []string{`{"t":"` + now.Format(time.RFC3339Nano) + `","kind":"recall","bytes":1}`, `{"t":"2026-08`},
			ok:    false,
		},
		{
			name:  "nothing at all",
			lines: []string{},
			ok:    false,
		},
	} {
		dir := t.TempDir()
		body := ""
		if len(c.lines) > 0 {
			body = strings.Join(c.lines, "\n") + "\n"
		}
		if err := os.WriteFile(Path(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, ok := oldestStampIn(Path(dir))
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		// Against the parser, not against a second copy of my arithmetic.
		var oldest time.Time
		for _, e := range read(Path(dir)) {
			if oldest.IsZero() || e.Time.Before(oldest) {
				oldest = e.Time
			}
		}
		if !got.Truncate(time.Second).Equal(oldest.Truncate(time.Second)) {
			t.Errorf("%s: scan says %v, the parser says %v", c.name, got, oldest)
		}
		if !got.Truncate(time.Second).Equal(c.want.Truncate(time.Second)) {
			t.Errorf("%s: scan says %v, want %v", c.name, got, c.want)
		}
	}
}
