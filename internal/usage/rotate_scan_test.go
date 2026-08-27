package usage

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

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
		for i := 0; i < n; i++ {
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
		for round := 0; round < 3; round++ {
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
	for round := 0; round < 3; round++ {
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
