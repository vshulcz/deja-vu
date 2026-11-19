package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The chart is a window, and a store that mostly predates it read as if the
// visible bar were the whole shape — while Range, two lines above, named
// months the chart never draws (#854).
func TestStatsSaysHowMuchIsOlderThanTheChart(t *testing.T) {
	months := make([]stats.MonthStats, 12)
	months[0] = stats.MonthStats{Month: "2025-09", Messages: 9}
	r := stats.Report{
		TotalSessions: 40, TotalMessages: 40,
		Monthly:   months,
		Sparkline: "█▁▁▁▁▁▁▁▁▁▁▁",
		DateRange: stats.DateRangeStats{Start: "2025-06-01", End: "2025-09-26"},
	}
	var out strings.Builder
	printStats(&out, r)
	got := out.String()
	if !strings.Contains(got, "31 of 40 messages are older than the chart") {
		t.Errorf("the chart does not say what it leaves out:\n%s", got)
	}

	// A store that fits inside the window says nothing: the chart is the whole
	// shape there, and a line about zero hidden messages is noise.
	inside := r
	inside.TotalMessages = 9
	inside.TotalSessions = 9
	var fits strings.Builder
	printStats(&fits, inside)
	if strings.Contains(fits.String(), "older than the chart") {
		t.Errorf("a store inside the window was told part of it is hidden:\n%s", fits.String())
	}

	// And a store where the window holds most of the work stays quiet too —
	// the line is for the case where the chart misleads, not for arithmetic.
	most := r
	most.TotalMessages = 12
	var mostOut strings.Builder
	printStats(&mostOut, most)
	if strings.Contains(mostOut.String(), "older than the chart") {
		t.Errorf("a mostly-visible store got the warning:\n%s", mostOut.String())
	}
}

func TestStatsRendersLocalCompactionRecoveryWithoutInventingABaseline(t *testing.T) {
	var out bytes.Buffer
	printStats(&out, stats.Report{
		TotalSessions: 1,
		Recall: usage.Summary{Compaction: &usage.CompactionSummary{
			Captures: 3, Measured: 2, Pending: 1, MedianActions: 3.5, P75Actions: 5,
		}},
	})
	got := out.String()
	for _, want := range []string{
		"After compaction 2 first edits · median 3.5 raw actions before edit · p75 5",
		"Compact samples   1 pending · 0 unmeasured",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stats missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"median 28", "p75 61"} {
		if strings.Contains(got, absent) {
			t.Errorf("stats invented maintainer baseline %q:\n%s", absent, got)
		}
	}
}

func TestStatsRendersUnmeasuredCompactionsWithoutSuccessMetric(t *testing.T) {
	var out bytes.Buffer
	printStats(&out, stats.Report{
		TotalSessions: 1,
		Recall: usage.Summary{Compaction: &usage.CompactionSummary{
			Captures: 2, Unmeasured: 2,
		}},
	})
	got := out.String()
	if !strings.Contains(got, "Compact samples   0 pending · 2 unmeasured") {
		t.Errorf("unmeasured state was not named:\n%s", got)
	}
	if strings.Contains(got, "After compaction") {
		t.Errorf("unmeasured state claimed a first-edit metric:\n%s", got)
	}
}

// The footer is the sentence someone reads before sharing the file, and it said
// no message text was in it while every row carried the opening line of a
// session (#2275).
func TestTheStatsPageDoesNotDenyCarryingTheTitles(t *testing.T) {
	withTempStores(t)
	dir := t.TempDir()
	page := filepath.Join(dir, "stats.html")
	if _, err := captureRun(t, "stats", "--html", page); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	// The premise: the page really does embed titles taken from message text.
	if !strings.Contains(s, `"title":"`) {
		t.Fatal("the page embeds no titles, so the footer has nothing to be wrong about")
	}
	if strings.Contains(s, "No message text is included in this file") {
		t.Error("the footer denies carrying message text while the titles are message text")
	}
	if !strings.Contains(s, "opening line") {
		t.Error("the footer does not say what the file holds")
	}
}

// The impact panel exists to answer "did the injected memory help". Counting
// only what was served cannot tell four helpful injections from four ignored
// ones — the credit line is the difference (#1062).
func TestImpactReportsCreditsNotJustServed(t *testing.T) {
	withStatsStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", "credit.jsonl"), "credit", []string{
		`{"type":"user","sessionId":"credit","timestamp":"2026-05-04T10:00:00Z","message":{"role":"user","content":"make the webhook stop hammering stripe"}}`,
		`{"type":"assistant","sessionId":"credit","timestamp":"2026-05-04T10:01:00Z","message":{"role":"assistant","content":"deja-vu recalled: we capped webhook retries at 3 — reusing that limit."}}`,
	})
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		usage.RecordResult(dir, usage.KindHook, 1000, 2, false)
	}

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `credited aloud     1 of 4 said "déjà vu"`) {
		t.Errorf("impact panel does not report credits:\n%s", out.String())
	}

	out.Reset()
	if err := runStatsImpact(&out, dir, true); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("bad json %q: %v", out.String(), err)
	}
	if got["credited_aloud"] != float64(1) {
		t.Errorf("credited_aloud = %v, want 1; keys: %v", got["credited_aloud"], keysOf(got))
	}
}

// Served with nothing credited must say so, not go silent — silence there reads
// as "no data" when it is really "no agent used it".
func TestImpactSaysWhenNothingWasCredited(t *testing.T) {
	withStatsStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 1000, 2, false)
	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `credited aloud     none of 1 yet`) {
		t.Errorf("impact panel is silent about zero credits:\n%s", out.String())
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestStatsCreditLineSingular(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", "credit.jsonl"), "credit", []string{
		`{"type":"assistant","sessionId":"credit","timestamp":"2026-05-04T10:01:00Z","message":{"role":"assistant","content":"deja-vu recalled: the webhook retry cap — reusing it."}}`,
	})
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"déjà vu" 1 time (`) {
		for line := range strings.SplitSeq(out, "\n") {
			if strings.Contains(line, "Credited aloud") {
				t.Fatalf("credit line: %q", line)
			}
		}
		t.Fatalf("no credit line in:\n%s", out)
	}
}

// The report and the credit count are marshalled apart and joined, because an
// embedded type's MarshalJSON answers for the whole outer struct. Joining is
// string surgery, so this pins the result: valid JSON, both keys, and no stray
// whitespace where the comma goes.
func TestImpactJSONIsWellFormedAfterTheJoin(t *testing.T) {
	var out strings.Builder
	r := usage.ImpactReport{Recalls: 3, Injections: 1, ServedBytes: 100, RawBytes: 1000, Since: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)}
	if err := printImpact(&out, r, 2, true); err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(out.String())) {
		t.Fatalf("not valid JSON:\n%s", out.String())
	}
	if strings.Contains(out.String(), "\n,") {
		t.Errorf("the join left a comma on its own line:\n%s", out.String())
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got["credited_aloud"] != float64(2) || got["since"] == nil || got["recalls"] != float64(3) {
		t.Errorf("keys lost in the join: %v", got)
	}
}

// n=1 is this screen's most common state, not its edge case: it is what a new
// user sees the first time an agent recalls anything, and it read "1
// agent-initiated recalls returned matches" (#1652).
func TestImpactScreenReadsAtOne(t *testing.T) {
	var b bytes.Buffer
	r := usage.ImpactReport{Recalls: 1, Injections: 1, DejaVuMoments: 1, ReusedTwice: 1, RawBytes: 400, ServedBytes: 200}
	if err := printImpact(&b, r, 0, false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, wrong := range []string{
		"1 agent-initiated recalls",
		"1 session starts",
		"1 prompts",
		"1 sessions recalled",
	} {
		if strings.Contains(out, wrong) {
			t.Errorf("reads %q:\n%s", wrong, out)
		}
	}
	for _, want := range []string{
		"1 agent-initiated recall ",
		"1 session start began",
		"1 prompt matched",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// The control: at two the plural comes back.
	b.Reset()
	r = usage.ImpactReport{Recalls: 2, Injections: 2, DejaVuMoments: 2, ReusedTwice: 2, RawBytes: 400, ServedBytes: 200}
	if err := printImpact(&b, r, 0, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 agent-initiated recalls", "2 session starts", "2 prompts matched"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("plural lost at two: missing %q:\n%s", want, b.String())
		}
	}
}

// creditedStore indexes one session in which an agent said "deja-vu recalled",
// and records enough usage for the impact screen to have something to explain.
func creditedStore(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-client")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"role":"assistant","content":"deja-vu recalled: we hit this retry bug in March — reusing that fix"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/client"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"aaa"})
	return dir
}

// Every surface that shows sessions consults the trust policy; this count did
// not, and it is derived from the text of sessions the reader is told they
// cannot see (#1354).
func TestImpactCreditsHonourTheTrustPolicy(t *testing.T) {
	dir := creditedStore(t)
	var before bytes.Buffer
	if err := runStatsImpact(&before, dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before.String(), "credited aloud     1 of") {
		t.Fatalf("the fixture does not produce a credit to withhold:\n%s", before.String())
	}
	writePolicy(t, `{"activations":{"search":{"local":false}}}`)
	var after bytes.Buffer
	if err := runStatsImpact(&after, dir, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after.String(), "credited aloud     1 of") {
		t.Errorf("a credit counted from a session the policy withholds:\n%s", after.String())
	}
	// The usage-log lines are events on this machine, not content from
	// elsewhere, so they stay.
	if !strings.Contains(after.String(), "recalls served") {
		t.Errorf("the policy emptied lines that come from this machine's own log:\n%s", after.String())
	}
}

// And with no rule in force the number is unchanged.
func TestImpactCreditsSurviveTheDefaultPolicy(t *testing.T) {
	dir := creditedStore(t)
	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "credited aloud     1 of") {
		t.Errorf("the default policy withheld a credit:\n%s", out.String())
	}
}

// The frame costs more than the text when sessions are short, and calling that
// "distilled (0× less)" claims a saving that did not happen (#731).
func TestImpactLineMatchesTheRatio(t *testing.T) {
	cases := []struct {
		name          string
		served        int
		raw           int64
		want, notWant string
	}{
		{"real distillation", 4_000, 200_000, "50× less", ""},
		{"barely any saving", 4_000, 5_000, "3.9 KB from 4.9 KB", "× less"},
		{"frame costs more", 1_404, 50, "the digest frame costs more", "context distilled"},
	}
	for _, c := range cases {
		var b strings.Builder
		r := usage.ImpactReport{Recalls: 1, ServedBytes: c.served, RawBytes: c.raw}
		if err := printImpact(&b, r, 0, false); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), c.want) {
			t.Errorf("%s: %q does not contain %q", c.name, b.String(), c.want)
		}
		if c.notWant != "" && strings.Contains(b.String(), c.notWant) {
			t.Errorf("%s: %q still says %q", c.name, b.String(), c.notWant)
		}
		if strings.Contains(b.String(), "(0× less)") {
			t.Errorf("%s printed an unreadable ratio: %q", c.name, b.String())
		}
	}
}

func TestStatsImpactCountsAndArithmetic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	usage.RecordServedSessions(dir, usage.KindRecall, 1000, 2, false, 50000, []string{"s1", "s2"})
	usage.RecordServedSessions(dir, usage.KindRecall, 500, 1, false, 25000, []string{"s1"})
	usage.RecordResultRaw(dir, usage.KindRecall, 0, 0, true, 0) // empty result must not count
	usage.RecordResultRaw(dir, usage.KindHook, 2000, 3, false, 100000)
	// A per-prompt injection carries a digest like the rest, so its bytes are
	// in the ratio (#2204).
	usage.RecordResult(dir, usage.KindDejaVu, 100, 1, false)

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"2 agent-initiated recalls",
		// Singular at one, as the rest of the screen already read (#1652).
		"1 session start began with project memory",
		"1 session recalled 2+ times",
		"1 prompt matched work",
		"49× less",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("impact output missing %q:\n%s", want, got)
		}
	}

	out.Reset()
	if err := runStatsImpact(&out, dir, true); err != nil {
		t.Fatal(err)
	}
	var r usage.ImpactReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Recalls != 2 || r.Injections != 1 || r.ReusedTwice != 1 || r.DejaVuMoments != 1 || r.ServedBytes != 3600 || r.RawBytes != 175000 {
		t.Fatalf("json report wrong: %+v", r)
	}
}

func TestStatsImpactEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := runStatsImpact(&out, filepath.Join(t.TempDir(), "index.db"), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no recall activity recorded yet") {
		t.Fatalf("empty state wrong:\n%s", out.String())
	}
}

// TestStatsImpactAgreesWithCount covers #1586: the impact screen hardcoded
// plural nouns, so a usage log holding exactly one of each — the state a new
// user is in — rendered "1 agent-initiated recalls returned matches".
func TestStatsImpactAgreesWithCount(t *testing.T) {
	// Every count is varied, and both directions are asserted. With only the
	// singular direction covered, hardcoding a noun the other way — dropping
	// pluralS from a line rather than adding it — was still invisible: the
	// suite stayed green through that mutation on two of the three lines.
	tests := []struct {
		name    string
		recalls int
		starts  int
		dejaVus int
		want    []string
		absent  []string
	}{
		{
			name:    "singular",
			recalls: 1, starts: 1, dejaVus: 1,
			want: []string{
				"1 agent-initiated recall returned matches",
				"1 session start began with project memory",
				"1 prompt matched work you had already done",
			},
			absent: []string{"1 agent-initiated recalls", "1 session starts", "1 prompts"},
		},
		{
			name:    "plural",
			recalls: 2, starts: 2, dejaVus: 2,
			want: []string{
				"2 agent-initiated recalls returned matches",
				"2 session starts began with project memory",
				"2 prompts matched work you had already done",
			},
			absent: []string{"2 agent-initiated recall ", "2 session start ", "2 prompt "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "index.db")
			for range tt.recalls {
				usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"s1"})
			}
			for range tt.starts {
				usage.RecordResultRaw(dir, usage.KindHook, 100, 1, false, 1000)
			}
			for range tt.dejaVus {
				usage.RecordResult(dir, usage.KindDejaVu, 100, 1, false)
			}

			var out bytes.Buffer
			if err := runStatsImpact(&out, dir, false); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("impact output missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("impact output still contains hardcoded plural %q:\n%s", absent, got)
				}
			}
		})
	}
}

// A filter that matches nothing is not an unbuilt index. `deja stats --since
// 30d` over a store whose sessions are all older answered "nothing indexed yet
// — run `deja index`", which is advice for a state deja is not in: indexing
// changes nothing and doctor reports the stores as found. `last` learned to say
// what emptied the result in #637; stats was the screen still guessing.
func TestStatsNamesTheFilterThatEmptiedIt(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "narrow", []string{
		`{"type":"user","sessionId":"narrow","timestamp":"2026-01-05T10:00:00Z",` +
			`"message":{"role":"user","content":"the migration ran twice"}}`,
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"stats", "--since", "30d"}, "since 30d"},
		{[]string{"stats", "--harness", "codex"}, `harness "codex"`},
		{[]string{"stats", "--project", "nowhere"}, `project "nowhere"`},
	} {
		out, err := captureRun(t, c.args...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "run `deja index`") {
			t.Errorf("%v sent the reader after a build that changes nothing:\n%s", c.args, out)
		}
		if !strings.Contains(out, "no sessions match "+c.want) {
			t.Errorf("%v does not name what emptied it:\n%s", c.args, out)
		}
	}

	// --since earns the extra line `last` prints: every session is older, and
	// the newest one dates the window the reader would have to widen to.
	out, err := captureRun(t, "stats", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2026-01-05") {
		t.Errorf("the window note does not date the newest session:\n%s", out)
	}

	// And an index with nothing in it keeps the advice that fits it, filter or
	// no filter — there the build is exactly what is missing.
	hermeticEnv(t)
	if err = index.Ensure(filepath.Join(t.TempDir(), "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "stats", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing indexed yet") {
		t.Errorf("an empty index lost the advice that fits it:\n%s", out)
	}
}

func setupNoIndexClaudeStore(t *testing.T, withSession bool) string {
	t.Helper()
	_ = hermeticEnv(t)
	nonexistentDir := filepath.Join(t.TempDir(), "never-created", "deja")
	t.Setenv("DEJA_DIR", nonexistentDir)
	t.Setenv("DEJA_INDEX_DIR", nonexistentDir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "xdg-cache"))
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	home := os.Getenv("HOME")
	claudeCfg := filepath.Join(home, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeCfg)
	if !withSession {
		return nonexistentDir
	}
	projDir := filepath.Join(claudeCfg, "projects", "-Users-alex-demo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"fix jwt key rotation"},"timestamp":"2026-10-01T10:00:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"rotated keys in auth middleware"},"timestamp":"2026-10-01T10:01:00Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(projDir, "session-1.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return nonexistentDir
}

func captureStatsCall(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var callErr error
	out := captureStdoutCall(t, func() {
		callErr = runStats(dir, args)
	})
	return out, callErr
}

func TestStatsNoIndexTextAndCardDoNotCreateDir(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, true)

	textOut, err := captureStatsCall(t, nonexistentDir, "--no-index")
	if err != nil {
		t.Fatalf("runStats --no-index: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created, got stat err: %v", serr)
	}
	if !strings.Contains(textOut, "local agent stores") {
		t.Fatalf("expected local agent stores subtitle, got:\n%s", textOut)
	}
	if strings.Contains(textOut, "Recall") {
		t.Fatalf("expected --no-index text report to omit Recall section, got:\n%s", textOut)
	}
	if !strings.Contains(textOut, "npx @vshulcz/deja-vu install --auto") {
		t.Fatalf("expected unwired install hint in text output, got:\n%s", textOut)
	}
	if !strings.Contains(textOut, "Sessions") || !strings.Contains(textOut, "1") || !strings.Contains(textOut, "2") {
		t.Fatalf("expected session and message counts in text output, got:\n%s", textOut)
	}

	cardOut, err := captureStatsCall(t, nonexistentDir, "--no-index", "--card")
	if err != nil {
		t.Fatalf("runStats --no-index --card: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created after --card, got stat err: %v", serr)
	}
	if !strings.Contains(cardOut, "npx @vshulcz/deja-vu install --auto") {
		t.Fatalf("expected unwired install hint in card output, got:\n%s", cardOut)
	}
}

func TestStatsNoIndexJSONDoesNotWriteIndexOrHint(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, true)

	jsonOut, err := captureStatsCall(t, nonexistentDir, "--no-index", "--json")
	if err != nil {
		t.Fatalf("runStats --no-index --json: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created after --json, got stat err: %v", serr)
	}
	if strings.Contains(jsonOut, "install --auto") {
		t.Fatalf("expected --json output not to contain install hint, got:\n%s", jsonOut)
	}
	var rep stats.Report
	if jerr := json.Unmarshal([]byte(jsonOut), &rep); jerr != nil {
		t.Fatalf("invalid json output: %v\n%s", jerr, jsonOut)
	}
	if rep.TotalSessions != 1 || rep.TotalMessages != 2 {
		t.Fatalf("unexpected report counts: sessions=%d messages=%d", rep.TotalSessions, rep.TotalMessages)
	}
}

func TestStatsNoIndexOmitsInstallHintOnceWired(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, true)

	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatalf("install claude-auto --no-index: %v", err)
	}

	textOut, err := captureStatsCall(t, nonexistentDir, "--no-index")
	if err != nil {
		t.Fatalf("runStats --no-index after install: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to exist when install used --no-index, got stat err: %v", serr)
	}
	if strings.Contains(textOut, "npx @vshulcz/deja-vu install --auto") {
		t.Fatalf("expected wired setup to omit install hint in text output, got:\n%s", textOut)
	}

	cardOut, err := captureStatsCall(t, nonexistentDir, "--no-index", "--card")
	if err != nil {
		t.Fatalf("runStats --no-index --card after install: %v", err)
	}
	if strings.Contains(cardOut, "npx @vshulcz/deja-vu install --auto") {
		t.Fatalf("expected wired setup to omit install hint in card output, got:\n%s", cardOut)
	}
}

func TestStatsNoIndexRejectsIncompatibleFlags(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, false)
	for _, flag := range []string{"--impact", "--year", "--redaction"} {
		if _, err := captureStatsCall(t, nonexistentDir, "--no-index", flag); err == nil {
			t.Fatalf("expected error for --no-index %s, got nil", flag)
		}
	}
}

func TestStatsNoIndexEmptyStoresMessage(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, false)

	out, err := captureStatsCall(t, nonexistentDir, "--no-index")
	if err != nil {
		t.Fatalf("runStats --no-index on empty stores: %v", err)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created on empty stores, got stat err: %v", serr)
	}
	if !strings.Contains(out, "no agent sessions found in local stores") {
		t.Fatalf("expected empty-store message, got:\n%s", out)
	}
}

func TestStatsNoIndexHarnessAndNarrowedFilter(t *testing.T) {
	nonexistentDir := setupNoIndexClaudeStore(t, true)

	claudeJSON, err := captureStatsCall(t, nonexistentDir, "--no-index", "--harness", "claude", "--json")
	if err != nil {
		t.Fatalf("runStats --no-index --harness claude --json: %v", err)
	}
	var rep stats.Report
	if jerr := json.Unmarshal([]byte(claudeJSON), &rep); jerr != nil {
		t.Fatalf("invalid json output: %v\n%s", jerr, claudeJSON)
	}
	if rep.TotalSessions != 1 || rep.TotalMessages != 2 {
		t.Fatalf("unexpected counts for --harness claude: sessions=%d messages=%d", rep.TotalSessions, rep.TotalMessages)
	}

	narrowOut, err := captureStatsCall(t, nonexistentDir, "--no-index", "--project", "nonexistent")
	if err != nil {
		t.Fatalf("runStats --no-index --project nonexistent: %v", err)
	}
	if !strings.Contains(narrowOut, "no sessions match") {
		t.Fatalf("expected narrowed filter message, got:\n%s", narrowOut)
	}
	if _, serr := os.Stat(nonexistentDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected index dir not to be created after filtered runs, got stat err: %v", serr)
	}
}

// stats is the one screen deja calls "wrapped for sharing", and it named the
// other machine's project while every other surface withheld it (#966).
func TestStatsObeysTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "secret/api", Role: "user", Text: "peer text"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err = index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "imported:secret") {
		t.Fatalf("the imported project is not in stats at all:\n%s", out)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err = os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err = captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "imported:secret") {
		t.Errorf("stats named a project the policy hides:\n%s", out)
	}
	note, err := captureRunStderr(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "trust policy hides") {
		t.Errorf("stats dropped sessions without saying why: %q", note)
	}
}

// The rule that emptied the report is named a line above, so "nothing indexed
// yet — run `deja index`" is advice for a state deja is not in: the backside
// `last` grew in #949 and stats kept (#983).
func TestStatsEmptiedByPolicyDoesNotAdviseAnIndexRun(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/api", Role: "user", Text: "peer work on the vault"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err = index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	policyFile := filepath.Join(tmp, "policy.json")
	if err = os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "nothing indexed yet") {
		t.Errorf("an index full of withheld sessions was called unbuilt:\n%s", out)
	}
	if !strings.Contains(out, "trust policy") {
		t.Errorf("stats does not name the rule that emptied it:\n%s", out)
	}

	// A machine with nothing indexed keeps the advice that fits it.
	tmp2 := hermeticEnv(t)
	dir2 := filepath.Join(tmp2, "index.db")
	if err = index.Ensure(dir2, "", false, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing indexed yet") {
		t.Errorf("an empty index lost its advice:\n%s", out)
	}
}

// `deja stats --project payments` narrowed the sessions and printed the
// machine's spans and recall figures beside them. The spans follow the filter;
// the recall figures cannot, and the screen says so.
func TestStatsProjectFilterNarrowsSpansAndLabelsRecall(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	edit := func(id, cwd, file string) {
		dir := filepath.Join(root, strings.ReplaceAll(cwd, "/", "-"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"shorten the pool lifetime"}}`, id, cwd, at),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"cfg.MaxConnLifetime = 30 * time.Minute","new_string":"cfg.MaxConnLifetime = 4 * time.Minute"}}]}}`, id, cwd, at, file),
		}
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("pay1", "/work/payments", "/work/payments/pool.go")
	edit("bill1", "/work/billing", "/work/billing/pool.go")
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	all, _, err := index.SpanInventoryWhere(dir, nil)
	if err != nil || all != 2 {
		t.Fatalf("premise: the store holds %d spans (%v), want 2", all, err)
	}
	some, _, err := index.SpanInventoryWhere(dir, func(m index.SessionMeta) bool { return m.ID == "pay1" })
	if err != nil || some != 1 {
		t.Fatalf("narrowed to one session: %d spans (%v), want 1", some, err)
	}

	var out bytes.Buffer
	printStats(&out, stats.Report{TotalSessions: 1, TotalMessages: 1, RecallMachineWide: true})
	if !strings.Contains(out.String(), "whole machine") {
		t.Fatalf("filtered stats printed machine-wide recall unlabelled:\n%s", out.String())
	}
}

// Three of the six spellings `deja help` documents came back empty on a store
// that holds those records. `--role tool` because stats compared the stored
// name to the documented alias; `--role command`, `--role files` and `--role
// edit` because the index withholds work records from a caller that did not ask
// for them and stats asked for everything, then narrowed afterwards (#3592).
func TestStatsReachesEveryRoleItDocuments(t *testing.T) {
	tmp := hermeticEnv(t)
	at := "2026-08-20T10:00:0%dZ"
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "roles", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"roles","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"the api will not start"}}`, fmt.Sprintf(at, 0)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"roles","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"docker compose up -d db"}}]}}`, fmt.Sprintf(at, 1)),
		fmt.Sprintf(`{"type":"user","sessionId":"roles","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Container db Started"}]}}`, fmt.Sprintf(at, 2)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"roles","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"assistant","content":[{"type":"text","text":"the database was down"}]}}`, fmt.Sprintf(at, 3)),
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	messages := regexp.MustCompile(`(?m)^Messages\s+(\d+)`)
	for _, role := range []string{"user", "assistant", "tool", "tool-output", "command"} {
		out, err := captureRun(t, "stats", "--role", role)
		if err != nil {
			t.Fatal(err)
		}
		m := messages.FindStringSubmatch(out)
		if m == nil {
			t.Errorf("stats --role %s found nothing on a store that holds those records:\n%s", role, out)
			continue
		}
		if m[1] == "0" {
			t.Errorf("stats --role %s counted zero:\n%s", role, out)
		}
	}

	// A role the store genuinely holds none of says so, rather than reporting
	// an unbuilt index — the whole point of letting the role reach the query is
	// that it empties the retrieval when it matches nothing.
	out, err := captureRun(t, "stats", "--role", "edit")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "run `deja index`") {
		t.Errorf("a role with no records was reported as an unbuilt index:\n%s", out)
	}
	if !strings.Contains(out, "holds no edit records") {
		t.Errorf("stats does not say the store records no edits:\n%s", out)
	}
}

var sgrRe = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// sgrOpenAtEnd reports whether the line leaves a colour or attribute switched
// on. Anything still open bleeds into the lines that follow it.
func sgrOpenAtEnd(line string) bool {
	open := false
	for _, m := range sgrRe.FindAllStringSubmatch(line, -1) {
		open = m[1] != "0" && m[1] != ""
	}
	return open
}

func TestStatHarnessTagClosesItsEscapes(t *testing.T) {
	for _, h := range []string{"claude", "codex", "opencode", "cursor", "gemini", "aider", "antigravity", "other"} {
		tag := statHarnessTag(h, true)
		if !strings.Contains(tag, "["+h+"]") {
			t.Fatalf("%s: tag lost its name: %q", h, tag)
		}
		if sgrOpenAtEnd(tag) {
			t.Fatalf("%s: tag ends with an attribute still open: %q", h, tag)
		}
	}
}

// The two stats lines that embed a harness tag: the counts after it and the
// line under it used to render bold because the tag re-armed bold with no
// closer.
func TestStatsHarnessLinesLeaveNothingOpen(t *testing.T) {
	byHarness := fmt.Sprintf("  %s%s %4d sessions  %5d messages", statHarnessTag("claude", true), strings.Repeat(" ", 7), 1, 24)
	longest := fmt.Sprintf("  Longest session  %d messages · %s · %s", 24, statHarnessTag("claude", true), "retry budget reset")
	for _, line := range []string{byHarness, longest} {
		if sgrOpenAtEnd(line) {
			t.Fatalf("line ends with an attribute still open: %q", line)
		}
	}
}

// "Recalls served" fell from 8100 to 101 when the log rotated, with nothing
// saying why (#763).
func TestStatsSaysSinceWhenTheLogHasAStart(t *testing.T) {
	var buf bytes.Buffer
	since := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	printStats(&buf, stats.Report{TotalSessions: 1, Recall: usage.Summary{Recalls: 101, Since: since}})
	if !strings.Contains(buf.String(), "Memory served    101 since "+search.DisplayDate(since)+" · 101 asked for by agents, 0 injected by hooks") {
		t.Errorf("output: %q", recallLine(buf.String()))
	}

	// No events, no period: an invented date would be worse than none. The
	// report needs a session for stats to print its body at all.
	buf.Reset()
	printStats(&buf, stats.Report{TotalSessions: 1, Recall: usage.Summary{}})
	if !strings.Contains(buf.String(), "Memory served    0\n") {
		t.Errorf("empty log: %q", recallLine(buf.String()))
	}
}

func recallLine(out string) string {
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, "Memory served") {
			return l
		}
	}
	return out
}

func TestMonthlyTotal(t *testing.T) {
	if got := monthlyTotal(nil); got != 0 {
		t.Errorf("nil = %d", got)
	}
	empty := make([]stats.MonthStats, 12)
	if got := monthlyTotal(empty); got != 0 {
		t.Errorf("twelve empty months = %d", got)
	}
	empty[3].Messages = 5
	empty[9].Messages = 2
	if got := monthlyTotal(empty); got != 7 {
		t.Errorf("total = %d, want 7", got)
	}
}

// Twelve empty bars is what a broken index looks like, and a store whose work
// is simply older than a year drew exactly that (#703).
func TestStatsSparklineSaysWhatItCoversWhenTheYearIsEmpty(t *testing.T) {
	var buf bytes.Buffer
	r := stats.Report{
		TotalSessions: 4,
		Monthly:       make([]stats.MonthStats, 12),
		Sparkline:     "▁▁▁▁▁▁▁▁▁▁▁▁",
		DateRange:     stats.DateRangeStats{Start: "2025-06-24", End: "2025-06-27"},
	}
	printStats(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "none — this store covers Jun 24 2025 → Jun 27 2025") {
		t.Errorf("empty year: %q", sparklineSection(out))
	}
	if strings.Contains(out, "▁▁▁▁▁▁▁▁▁▁▁▁") {
		t.Errorf("still drew the empty sparkline: %q", sparklineSection(out))
	}

	// A store with work inside the window keeps its chart.
	buf.Reset()
	r.Monthly[10].Messages = 8
	r.Sparkline = "▁▁▁▁▁▁▁▁▁▁█▁"
	printStats(&buf, r)
	if !strings.Contains(sparklineSection(buf.String()), "###") {
		t.Errorf("chart lost: %q", sparklineSection(buf.String()))
	}
}

func sparklineSection(out string) string {
	i := strings.Index(out, "Last 12 months")
	if i < 0 {
		return out
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n\n"); j > 0 {
		return rest[:j]
	}
	return rest
}

// Each month's name sits under its own bar: the chart used to be twelve
// one-cell bars followed by twelve labels, so no label was under its bar.
func TestMonthChartPutsEachLabelUnderItsBar(t *testing.T) {
	months := make([]stats.MonthStats, 12)
	for i := range months {
		months[i].Month = fmt.Sprintf("2026-%02d", i+1)
	}
	months[2].Messages = 10
	lines := monthChart(months, false)
	bottom, labels := lines[len(lines)-2], lines[len(lines)-1]
	at := strings.Index(bottom, "#")
	if at < 0 || labels[at:at+3] != "Mar" || bottom[at:at+3] != "###" {
		t.Errorf("bar and label disagree:\n%s\n%s", bottom, labels)
	}
}

// The card is an SVG document. Writing it into card.png produced a file
// GitHub serves as a PNG and browsers refuse, while the command printed the
// markdown to embed it — a broken image for anyone who followed the hint.

func TestCardFileNameKeepsTheFormatHonest(t *testing.T) {
	// An extension deja cannot honour is replaced, not appended: card.png used
	// to become card.png.svg, an SVG named after a format it is not (#1056).
	for in, want := range map[string]string{
		"deja-stats.svg": "deja-stats.svg",
		"CARD.SVG":       "CARD.SVG",
		"card.png":       "card.svg",
		"card.txt":       "card.svg",
		"card":           "card.svg",
		"dir/card":       "dir/card.svg",
		"dir/card.jpeg":  "dir/card.svg",
	} {
		got, note := cardFileName(in)
		if got != want {
			t.Fatalf("cardFileName(%q) = %q, want %q", in, got, want)
		}
		// The replacement is stated once; a name deja could keep says nothing.
		if wantNote := filepath.Ext(in) != "" && !strings.EqualFold(filepath.Ext(in), ".svg"); wantNote != (note != "") {
			t.Errorf("cardFileName(%q) note = %q, want a note: %v", in, note, wantNote)
		}
	}
}

// `deja restore` matters at one panicked moment, and nobody reads a command
// list then. The number is stated where someone reads calmly, and it is their
// own data (#577).
func TestStatsNamesWhatRestoreCanRecover(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-s")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"sp1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"fix the pool"}}` + "\n" +
		`{"type":"assistant","sessionId":"sp1","cwd":"/w","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/pool.go","old_string":"the bytes that stopped existing","new_string":"new"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "sp1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Recover") || !strings.Contains(out, "1 span your agents replaced") {
		t.Fatalf("stats does not say what can be recovered:\n%s", out)
	}
	if !strings.Contains(out, "deja restore") {
		t.Fatalf("the command is not named:\n%s", out)
	}
	// Singular reads right: "1 spans across 1 files" is the kind of thing that
	// makes a page look unfinished.
	if strings.Contains(out, "1 spans") || strings.Contains(out, "1 files") {
		t.Fatalf("plural disagreement:\n%s", out)
	}
}

// A store with nothing to recover says nothing: a zero here is an invitation
// to a command that would answer nothing.
func TestStatsSilentWithNoSpans(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-n")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"n1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"just talking"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "n1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Recover") || strings.Contains(out, "deja restore") {
		t.Fatalf("offered recovery on a store with nothing to recover:\n%s", out)
	}
}

// The note says a rule hides matching sessions, and stats counted every hidden
// session in the store — so `deja stats --project alpha` reported the sessions
// hidden in beta as matching ones (#2816, the class #2766 and #2794 fixed for
// how and friction).
func TestStatsCountsTheWithheldSessionsItsFiltersWouldShow(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, p := range []struct{ project, id string }{{"alpha", "aaaa0001"}, {"beta", "bbbb0001"}} {
		dir := filepath.Join(root, "-"+p.project)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","timestamp":"2026-07-10T10:00:00Z","sessionId":"` + p.id +
			`-1111-4000-8000-d6e7f8a9b0c1","cwd":"/` + p.project +
			`","message":{"role":"user","content":"the pool is too small"}}`
		if err := os.WriteFile(filepath.Join(dir, p.id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"local":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	note, err := captureRunStderr(t, "stats", "--project", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "hides 1 matching session") {
		t.Errorf("one hidden session is in alpha; the note counts the store:\n%s", note)
	}

	// And without a filter the number is the store's again.
	note, err = captureRunStderr(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "hides 2 matching sessions") {
		t.Errorf("with no filter both hidden sessions match:\n%s", note)
	}
}

// The two rules #578 puts on this screen, and they are the whole point of it:
// every number says the arithmetic behind it, and nothing leaves for a screen
// meant to be shown to other people without the outbound redaction pass.
func TestYearScreenLabelsItsArithmeticAndRedactsWhatItQuotes(t *testing.T) {
	dir := yearFixture(t)
	var out bytes.Buffer
	if err := runStatsYear(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	for _, want := range []string{
		"deduplicated by path",
		"deduplicated by the command line",
		"asked in more than one session",
		"the exact bytes an edit overwrote",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the screen prints a number without the arithmetic behind it — no %q:\n%s", want, got)
		}
	}
	// The friction line the fixture plants carries a home path and an internal
	// hostname, which is exactly what a screen written to be pasted must not
	// carry.
	if strings.Contains(got, "/Users/dana") || strings.Contains(got, "build.internal") {
		t.Errorf("the year screen quotes a home path or an internal host:\n%s", got)
	}
	if !strings.Contains(got, "masked for outbound use") {
		t.Errorf("the screen masked something and did not say so:\n%s", got)
	}
	// And it says what it quoted at all, or the redaction check above passes
	// on an empty screen.
	if !strings.Contains(got, "sessions hit") {
		t.Errorf("no recurring error reached the screen, so the redaction is unproven:\n%s", got)
	}
}

func TestPrintYearPluralizesSingleCounts(t *testing.T) {
	var out bytes.Buffer
	printYear(&out, yearReport{
		from:          time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
		to:            time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC),
		sessions:      1,
		messages:      1,
		harnesses:     []harnessYear{{Harness: "claude", Sessions: 1}},
		projects:      []projectYear{{Project: "my/app", Sessions: 1}},
		work:          index.YearWork{Records: map[string]int{"files": 1, "command": 1}, Files: 1, Commands: 1, Undated: 1},
		questions:     questionYear{Distinct: 1, Repeated: 1},
		busiestDay:    "not-a-date",
		busiestCount:  1,
		longestTitle:  "Fix frobnicator",
		longestTurns:  1,
		friction:      []frictionYear{{Line: "error", Sessions: 1}},
		outsideWindow: 1,
	})
	got := out.String()

	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "headline session and turns", want: "1 session, 1 turn in them"},
		{name: "project sessions", want: "my/app, 1 session"},
		{name: "busiest day turns", want: "not-a-date, 1 turn"},
		{name: "longest session turns", want: "1 turn — Fix frobnicator"},
		{name: "file records", want: "1 record naming a file"},
		{name: "command records", want: "1 command record"},
		{name: "undated work record", want: "1 work record carries no timestamp, so it is in none"},
		{name: "repeated question", want: "1 of 1 question was asked in more than one session"},
		{name: "friction sessions", want: "1 session hit"},
		{name: "sessions outside the window", want: "1 indexed session is older"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(got, tc.want) {
				t.Errorf("year report is missing %q:\n%s", tc.want, got)
			}
		})
	}

	for _, bad := range []string{"1 sessions", "1 turns", "1 records", "1 command records", "of 1 questions", "1 work records", "1 indexed sessions"} {
		if strings.Contains(got, bad) {
			t.Errorf("year report has incorrect singular count %q:\n%s", bad, got)
		}
	}
}

func TestYearJSONCarriesTheSameNumbers(t *testing.T) {
	dir := yearFixture(t)
	var out bytes.Buffer
	if err := runStatsYear(&out, dir, true); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Kind      string `json:"kind"`
		Sessions  int    `json:"sessions"`
		Questions struct {
			Distinct int `json:"distinct"`
			Repeated int `json:"repeated"`
		} `json:"questions"`
		Work struct {
			Files    int `json:"files"`
			Commands int `json:"commands"`
			Spans    int `json:"spans"`
		} `json:"work"`
		Friction []struct {
			Line     string `json:"line"`
			Sessions int    `json:"sessions"`
		} `json:"friction"`
		Masked map[string]int `json:"masked"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("the JSON does not parse: %v\n%s", err, out.String())
	}
	if got.Kind != yearJSONKind {
		t.Errorf("kind = %q, want %q", got.Kind, yearJSONKind)
	}
	if got.Sessions == 0 || got.Work.Files == 0 || got.Work.Spans == 0 {
		t.Errorf("the JSON is empty where the screen is not: %+v", got)
	}
	if got.Questions.Repeated < 1 || got.Questions.Distinct <= got.Questions.Repeated {
		t.Errorf("questions = %d repeated of %d distinct, want a repeat inside a larger population",
			got.Questions.Repeated, got.Questions.Distinct)
	}
	if len(got.Friction) == 0 {
		t.Fatalf("no friction in the JSON: %s", out.String())
	}
	for _, f := range got.Friction {
		if strings.Contains(f.Line, "/Users/dana") || strings.Contains(f.Line, "build.internal") {
			t.Errorf("the JSON carries what the screen masked: %q", f.Line)
		}
	}
	if len(got.Masked) == 0 {
		t.Errorf("nothing reported as masked, though the fixture plants two shapes: %s", out.String())
	}
}

// A filter would narrow the sessions and not the records, so the screen would
// stop adding up. It refuses rather than printing a report half-filtered.
func TestYearRefusesFiltersAndTheOtherReports(t *testing.T) {
	dir := yearFixture(t)
	t.Setenv("DEJA_INDEX_DIR", dir)
	for _, args := range [][]string{
		{"--year", "--harness", "claude"},
		{"--year", "--since", "30d"},
		{"--year", "--project", "app"},
		{"--year", "--role", "command"},
		{"--year", "--impact"},
		{"--year", "--card"},
	} {
		err := runStats(dir, args)
		if err == nil {
			t.Errorf("deja stats %v was accepted", args)
			continue
		}
		if !strings.Contains(err.Error(), "--year takes no filters") && !strings.Contains(err.Error(), "two reports") &&
			!strings.Contains(err.Error(), "one output") {
			t.Errorf("deja stats %v refused with %q, which does not say why", args, err)
		}
	}
}

// yearFixture is a year's worth of three sessions: the same file edited twice,
// the same command run in each, one question asked in all three, and an error line
// carrying a home path and an internal hostname.
func yearFixture(t *testing.T) string {
	t.Helper()
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	fail := `make: *** [build] Error 1 in /Users/dana/src/app, reported by build.internal`
	for i, id := range []string{"y1", "y2", "y3"} {
		// Each session asks at its own time: the same question at the same
		// moment in two sessions is one conversation copied, not a repeat.
		ts := time.Now().Add(-48*time.Hour + time.Duration(i)*time.Hour).UTC().Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"user","content":"why does the payout retry fire twice in a row"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make build"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"` + fail + `"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/retry.go","old_string":"for i := 0; i < attempts; i++ {","new_string":"for i := 0; i <= attempts; i++ {"}}]}}`,
		}
		// A second question, asked once, so the repeat has a population to sit
		// in: "1 of 1 asked twice" is not a figure about anything.
		if id == "y3" {
			lines = append(lines, `{"type":"user","sessionId":"y3","cwd":"/work/app","timestamp":"`+ts+
				`","message":{"role":"user","content":"where does the scheduler read its failover timeout from"}}`)
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", id+".jsonl"), id, lines)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
