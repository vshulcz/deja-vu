package usage

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func usageDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// Impact is what `deja stats --impact` prints, and the README promises those
// are counted numbers rather than modelled ones — so the counting is worth
// pinning.
func TestImpactCountsEachKindOnce(t *testing.T) {
	dir := usageDir(t)
	RecordResultRaw(dir, KindRecall, 100, 2, false, 10_000)
	RecordResultRaw(dir, KindRecall, 50, 1, false, 5_000)
	RecordResultRaw(dir, KindContext, 30, 1, false, 3_000)
	RecordResultRaw(dir, KindHook, 20, 1, false, 2_000)
	RecordResultRaw(dir, KindDejaVu, 10, 1, false, 1_000)
	// An empty recall served nothing and must not inflate the numbers.
	RecordResultRaw(dir, KindRecall, 0, 0, true, 0)

	got := Impact(dir)
	if got.Recalls != 3 {
		t.Fatalf("recalls = %d, want the three non-empty ones", got.Recalls)
	}
	if got.Injections != 1 {
		t.Fatalf("injections = %d", got.Injections)
	}
	if got.DejaVuMoments != 1 {
		t.Fatalf("déjà vu moments = %d", got.DejaVuMoments)
	}
	// Every door that carried a digest, the per-prompt one included: it is
	// distilled from transcripts like the rest, and leaving it out computed the
	// ratio from a subset of what deja served (#2204).
	if got.ServedBytes != 210 {
		t.Fatalf("served bytes = %d, want 100+50+30+20+10", got.ServedBytes)
	}
	if got.RawBytes != 21_000 {
		t.Fatalf("raw bytes = %d", got.RawBytes)
	}
}

// "Reused twice" is the number that claims a fix kept paying, so it must count
// sessions rather than events.
func TestImpactCountsSessionsReusedTwice(t *testing.T) {
	dir := usageDir(t)
	RecordServedSessions(dir, KindRecall, 10, 1, false, 0, []string{"a"})
	RecordServedSessions(dir, KindRecall, 10, 1, false, 0, []string{"a", "b"})
	RecordServedSessions(dir, KindRecall, 10, 1, false, 0, []string{"c"})
	if got := Impact(dir).ReusedTwice; got != 1 {
		t.Fatalf("reused twice = %d, want only the session seen in two recalls", got)
	}
}

func TestDejaVuWeekCountsOnlyRecentNonEmpty(t *testing.T) {
	dir := usageDir(t)
	RecordResultRaw(dir, KindDejaVu, 10, 1, false, 0)
	// An empty moment is not a moment.
	RecordResultRaw(dir, KindDejaVu, 0, 0, true, 0)
	if got := DejaVuWeek(dir); got != 1 {
		t.Fatalf("this week = %d, want 1", got)
	}
	// Anything older than the window drops out; write one by hand since the
	// recorder always stamps now.
	appendEventForTest(t, dir, Event{Kind: KindDejaVu, Time: time.Now().AddDate(0, 0, -30), Sessions: 1})
	if got := DejaVuWeek(dir); got != 1 {
		t.Fatalf("a month-old moment counted as this week: %d", got)
	}
}

func TestTodayRawSumsOnlyTodaysServedKinds(t *testing.T) {
	dir := usageDir(t)
	RecordResultRaw(dir, KindRecall, 10, 1, false, 1_000)
	RecordResultRaw(dir, KindHook, 10, 1, false, 2_000)
	// A kind that serves nothing must not add to the volume it distilled.
	RecordResultRaw(dir, KindSearch, 0, 0, false, 9_999)
	if got := TodayRaw(dir); got != 3_000 {
		t.Fatalf("today raw = %d, want 1000+2000", got)
	}
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: time.Now().AddDate(0, 0, -2), RawBytes: 5_000, Sessions: 1})
	if got := TodayRaw(dir); got != 3_000 {
		t.Fatalf("an older entry leaked into today: %d", got)
	}
}

// The snapshot variants differ only in what they carry alongside the text;
// all of them must leave the digest where `deja log` reads it.
func TestSnapshotVariantsWriteTheDigest(t *testing.T) {
	for name, record := range map[string]func(string){
		"RecordDigestTerms": func(d string) { RecordDigestTerms(d, KindRecall, "digest terms", 1, 10, []string{"etag", "ttl"}) },
		"SnapshotOnly":      func(d string) { SnapshotOnly(d, KindRecall, "digest only", 1) },
		"SnapshotPolicy":    func(d string) { SnapshotPolicy(d, KindRecall, "digest policy", 1, "local") },
	} {
		dir := usageDir(t)
		record(dir)
		b, err := os.ReadFile(SnapshotPath(dir))
		if err != nil {
			t.Fatalf("%s: no snapshot written: %v", name, err)
		}
		if !strings.Contains(string(b), "digest") {
			t.Fatalf("%s: snapshot missing the digest text: %s", name, b)
		}
	}
	// An empty digest is not worth a snapshot line.
	dir := usageDir(t)
	SnapshotOnly(dir, KindRecall, "", 0)
	if _, err := os.Stat(SnapshotPath(dir)); !os.IsNotExist(err) {
		t.Fatal("empty digest produced a snapshot")
	}
}

func TestRecordDigestTermsKeepsTheTerms(t *testing.T) {
	dir := usageDir(t)
	RecordDigestTerms(dir, KindDejaVu, "the digest", 2, 500, []string{"etag", "jitter"})
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"etag", "jitter"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("term %q not kept for the audit trail: %s", want, b)
		}
	}
	// And the counting event went in too, or stats would under-report.
	if got := Impact(dir).DejaVuMoments; got != 1 {
		t.Fatalf("déjà vu moments = %d", got)
	}
}

// appendEventForTest writes an event with a chosen timestamp: the recorder
// always stamps now, and these tests are about the windows.
func appendEventForTest(t *testing.T, dir string, e Event) {
	t.Helper()
	f, err := os.OpenFile(Path(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// The log rotates so it cannot grow without bound, and what it keeps is the
// recent window — dropping the wrong side would erase the history stats are
// counted from.
func TestRotateKeepsTheRecentWindow(t *testing.T) {
	dir := usageDir(t)
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: time.Now().UTC(), Bytes: 1, Sessions: 1})
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: time.Now().UTC().Add(-2 * keepWindow), Bytes: 1, Sessions: 1})
	// Pad past the rotation threshold so the next write triggers it.
	pad := Event{Kind: KindSearch, Time: time.Now().UTC(), Bytes: 1}
	for size := int64(0); size < rotateAt; {
		appendEventForTest(t, dir, pad)
		fi, err := os.Stat(Path(dir))
		if err != nil {
			t.Fatal(err)
		}
		size = fi.Size()
	}
	RecordResultRaw(dir, KindRecall, 5, 1, false, 0)

	var recent, old int
	for _, e := range read(Path(dir)) {
		if e.Time.Before(time.Now().UTC().Add(-keepWindow)) {
			old++
		} else {
			recent++
		}
	}
	if old != 0 {
		t.Fatalf("rotation kept %d entries older than the window", old)
	}
	if recent == 0 {
		t.Fatal("rotation dropped everything")
	}
}

// WornSessions feeds ranking: a session an agent recalled twice should weigh
// more. Kinds that are not agent-initiated recalls must not contribute.
func TestWornSessionsCountsOnlyRecalls(t *testing.T) {
	dir := usageDir(t)
	if got := WornSessions(dir); got != nil {
		t.Fatalf("empty log returned %v, want nil so callers can skip the work", got)
	}
	RecordServedSessions(dir, KindRecall, 10, 1, false, 0, []string{"a", "b"})
	RecordServedSessions(dir, KindContext, 10, 1, false, 0, []string{"a"})
	RecordServedSessions(dir, KindHook, 10, 1, false, 0, []string{"c"})
	got := WornSessions(dir)
	if got["a"] != 2 || got["b"] != 1 {
		t.Fatalf("worn counts wrong: %v", got)
	}
	if _, ok := got["c"]; ok {
		t.Fatal("a session-start injection counted as agent-initiated re-use")
	}
}
