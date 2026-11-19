package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A session start in a project with no history still injects the environment
// block, and the hook logs that event empty. Impact counted it, so a machine
// that had never recalled a single project session reported "1 session starts
// began with project memory".
func TestImpactSkipsHooksThatCarriedNoSession(t *testing.T) {
	dir := usageDir(t)
	now := time.Now()
	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 423, Empty: true})

	got := Impact(dir)
	if got.Injections != 0 {
		t.Errorf("injections = %d, want 0 — the event carried no project memory", got.Injections)
	}
	if got.ServedBytes != 0 {
		t.Errorf("served bytes = %d, want 0", got.ServedBytes)
	}

	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 500, Sessions: 2, RawBytes: 4000})
	if got := Impact(dir); got.Injections != 1 || got.ServedBytes != 500 || got.RawBytes != 4000 {
		t.Errorf("a real injection stopped counting: %+v", got)
	}
}

// The impact line reads "N served instead of M of raw transcripts". The
// environment block has no transcripts behind it — it is a summary of what this
// machine keeps hitting, recorded with no raw size — so its bytes would raise
// that numerator against an unchanged denominator and understate the saving.
// #1963 proposed counting them; these are the numbers that say otherwise.
func TestTheImpactRatioIgnoresTheEnvironmentBlock(t *testing.T) {
	dir := t.TempDir()
	for range 3 {
		RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	}
	before := Impact(dir)

	for range 10 {
		RecordResultRaw(dir, KindHook, 501, 0, true, 0)
	}
	after := Impact(dir)

	if after.ServedBytes != before.ServedBytes || after.RawBytes != before.RawBytes {
		t.Errorf("ten environment blocks moved the ratio: served %d→%d, raw %d→%d",
			before.ServedBytes, after.ServedBytes, before.RawBytes, after.RawBytes)
	}
	if after.Injections != 0 {
		t.Errorf("injections = %d; no session start here began with project memory", after.Injections)
	}
	// And the real work is still counted, so this is not a test that passes on
	// an empty report.
	if after.ServedBytes != 2700 || after.RawBytes != 27000 {
		t.Errorf("the recalls stopped counting: served=%d raw=%d", after.ServedBytes, after.RawBytes)
	}
}

// The impact report is a ratio: what deja served against the transcripts behind
// it. The per-prompt recall and the tool-time line are digests distilled from
// real transcripts, and both were dropped whole — bytes and raw — so the ratio
// was computed from a subset of what deja handed over. The documented exclusion
// is narrower: the environment block, which is an empty hook event carrying a
// summary of the machine rather than a digest (#2204).
func TestImpactCountsEveryInjectionThatCarriedADigest(t *testing.T) {
	dir := t.TempDir()
	RecordResultRaw(dir, KindHook, 600, 1, false, 6000)
	RecordResultRaw(dir, KindDejaVu, 500, 1, false, 5000)
	RecordResultRaw(dir, KindTool, 400, 1, false, 4000)
	RecordResultRaw(dir, KindRecall, 300, 1, false, 3000)

	tot := Totals(dir)
	imp := Impact(dir)
	// The premise: `deja stats` counts all four, so the two reports are about
	// the same events and any gap below is this report's own.
	if tot.Bytes != 1800 || tot.RawBytes != 18000 {
		t.Fatalf("stats counts %d B over %d raw, so this measures nothing", tot.Bytes, tot.RawBytes)
	}
	if imp.ServedBytes != 1800 || imp.RawBytes != 18000 {
		t.Errorf("impact counts %d B over %d raw; the two reports disagree about what deja served",
			imp.ServedBytes, imp.RawBytes)
	}

	// The environment block stays out of both halves, which is what the report
	// documents: it is a summary of the machine, not a digest of transcripts.
	quiet := t.TempDir()
	RecordResultRaw(quiet, KindHook, 700, 0, true, 0)
	RecordResultRaw(quiet, KindDejaVu, 800, 0, true, 0)
	if r := Impact(quiet); r.ServedBytes != 0 || r.Injections != 0 {
		t.Errorf("an injection that carried no session was counted: %d B, %d injections", r.ServedBytes, r.Injections)
	}

	// And `injections` keeps the meaning the docs give it — session starts that
	// began with project memory — rather than growing to mean every door.
	if imp.Injections != 1 {
		t.Errorf("injections is %d; the documented count is session starts, of which there was one", imp.Injections)
	}
}

// writeImpactLog writes events at the given ages in days, all recalls.
func writeImpactLog(t *testing.T, ages ...int) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var b strings.Builder
	for i, age := range ages {
		e := Event{
			Time: now.Add(-time.Duration(age) * 24 * time.Hour), Kind: KindRecall,
			Bytes: 100, Sessions: 1, RawBytes: 1000, SessionIDs: []string{string(rune('a' + i))},
		}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The impact screen reads as everything that has happened on this machine,
// while the log it counts is rewritten past 1MB keeping 14 days — so the
// figures are a window whose start moves silently, and one rotation halved
// them. `deja stats` has said which window it means since #763; this is the
// same fact on the screen that most looks like a lifetime total (#1889).
func TestImpactCarriesTheWindowItCounted(t *testing.T) {
	dir := writeImpactLog(t, 20, 10, 1)
	r := Impact(dir)
	if r.Recalls != 3 {
		t.Fatalf("counted %d recalls, want 3", r.Recalls)
	}
	if r.Since.IsZero() {
		t.Fatal("the report does not say when its window opens")
	}
	if age := time.Since(r.Since).Hours() / 24; age < 19 || age > 21 {
		t.Errorf("the window opens %.0f days ago, want the oldest event at 20", age)
	}
}

// A log with nothing in it says nothing rather than year 1 (the shape of
// #1874).
func TestAnEmptyImpactLogHasNoWindow(t *testing.T) {
	dir := writeImpactLog(t)
	r := Impact(dir)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "since") {
		t.Errorf("an empty log reports a window: %s", b)
	}
}

// And a log with events carries it in the machine form too.
func TestImpactJSONCarriesTheWindow(t *testing.T) {
	dir := writeImpactLog(t, 5, 2)
	b, err := json.Marshal(Impact(dir))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["since"]; !ok {
		t.Errorf("--impact --json does not say which window it counted: %s", b)
	}
}
