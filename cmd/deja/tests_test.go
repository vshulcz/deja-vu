package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// run is one build or test invocation in a transcript: the command, then the
// output that came back.
func testRunLines(session, cwd, day, id, cmd, out string) []string {
	ts := day + "T10:00:00Z"
	return []string{
		`{"type":"assistant","sessionId":"` + session + `","cwd":"` + cwd + `","timestamp":"` + ts +
			`","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"Bash",` +
			`"input":{"command":` + jsonQuoted(cmd) + `}}]}}`,
		`{"type":"user","sessionId":"` + session + `","cwd":"` + cwd + `","timestamp":"` + ts +
			`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id +
			`","content":` + jsonQuoted(out) + `}]}}`,
	}
}

// jsonQuoted writes a string the way a transcript holds it, newlines and all.
func jsonQuoted(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The band that decides whether the rest of the screen is honest. An agent
// almost always pipes its own run through a filter — `grep -E '^FAIL'` and an
// `echo DONE` after it — and the verdict never reaches the transcript. Counting
// those as passes was what made the first measurement of this say the suite
// fails one run in five when the runs that answered say two in five (#539).
func TestAFilteredRunIsCountedAsNeitherPassNorFail(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	var lines []string
	lines = append(lines, testRunLines("r1", "/work/app", "2026-03-02", "c1",
		"go test ./internal/fetch -count=1", "ok  \tgithub.com/me/app/internal/fetch\t1.2s")...)
	// The same command line as the run above on purpose: a session repeating
	// one verbatim keeps a single command record and both outputs, and the
	// second run has to survive that (#539).
	lines = append(lines, testRunLines("r1", "/work/app", "2026-03-02", "c2",
		"go test ./internal/fetch -count=1", "--- FAIL: TestRetryBudget (0.01s)\nFAIL\tgithub.com/me/app/internal/fetch\t0.9s\nFAIL")...)
	lines = append(lines, testRunLines("r1", "/work/app", "2026-03-02", "c3",
		`go test ./... -count=1 2>&1 | grep -E "^FAIL" | head -3; echo DONE`, "DONE")...)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "r1.jsonl"), "r1", lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	h, err := index.ScanTestHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Runs != 3 {
		t.Fatalf("want 3 runs, got %d (%+v)", h.Runs, h)
	}
	if h.Passed != 1 || h.Failed != 1 || h.NoVerdict != 1 {
		t.Fatalf("want one of each band, got passed=%d failed=%d no-verdict=%d", h.Passed, h.Failed, h.NoVerdict)
	}
	var out bytes.Buffer
	if err := runTests(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "no verdict") {
		t.Errorf("the screen does not name the third band:\n%s", got)
	}
	// The rate has to be stated over the runs it was measured on, not over
	// every run: 1 of 2, not 1 of 3.
	if !strings.Contains(got, "1 of the 2 runs that carried a verdict failed (50%)") {
		t.Errorf("the screen does not state the rate over the runs that answered:\n%s", got)
	}
}

// A test that failed twelve times in one afternoon was being fixed. One that
// failed on nine separate days is the one worth knowing about, so the list is
// cut on days and not on failures.
func TestOnlyATestThatFailedOnTwoDaysIsListed(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	fail := func(name string) string {
		return "--- FAIL: " + name + " (0.02s)\nFAIL\tgithub.com/me/app/internal/fetch\t0.4s\nFAIL"
	}
	var lines []string
	// Twice in one day.
	lines = append(lines, testRunLines("r2", "/work/app", "2026-03-03", "a1",
		"go test ./internal/fetch", fail("TestQuokkabloomOnce"))...)
	lines = append(lines, testRunLines("r2", "/work/app", "2026-03-03", "a2",
		"go test ./internal/fetch", fail("TestQuokkabloomOnce"))...)
	// Once on each of two days.
	lines = append(lines, testRunLines("r2", "/work/app", "2026-03-03", "a3",
		"go test ./internal/fetch", fail("TestQuokkabloomKeepsComingBack"))...)
	lines = append(lines, testRunLines("r2", "/work/app", "2026-03-05", "a4",
		"go test ./internal/fetch", fail("TestQuokkabloomKeepsComingBack"))...)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "r2.jsonl"), "r2", lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	h, err := index.ScanTestHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Repeats) != 1 {
		t.Fatalf("want one repeat-failing test, got %d: %+v", len(h.Repeats), h.Repeats)
	}
	r := h.Repeats[0]
	if r.Name != "TestQuokkabloomKeepsComingBack" || r.Days != 2 || r.Failures != 2 {
		t.Fatalf("wrong row: %+v", r)
	}
	var out bytes.Buffer
	if err := runTests(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "TestQuokkabloomKeepsComingBack") {
		t.Errorf("the screen does not name the test that kept failing:\n%s", got)
	}
	if strings.Contains(got, "TestQuokkabloomOnce") {
		t.Errorf("the screen named a test that only failed on one day:\n%s", got)
	}
}

func TestTestsJSONCarriesTheSeriesAndTheFlagsAreChecked(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	var lines []string
	for i, day := range []string{"2026-03-09", "2026-03-10", "2026-03-17"} {
		lines = append(lines, testRunLines("r3", "/work/app", day, fmt.Sprintf("j%d", i),
			"go build ./...", "# github.com/me/app\ninternal/fetch/fetch.go:12:3: undefined: quokkabloom")...)
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "r3.jsonl"), "r3", lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runTests(dir, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var got testsJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.Kind != "deja.tests" || got.Schema == 0 {
		t.Errorf("no kind or schema version: %+v", got)
	}
	// A compile error is a failure: the suite did not run, which is not a pass
	// by any reading. The `#` line above it used to win as a `go build` pass.
	if got.Failed != 3 || got.Passed != 0 {
		t.Errorf("a compile error was not counted as a failure: %+v", got)
	}
	if len(got.Weeks) != 2 {
		t.Errorf("want two weeks in the series, got %d: %+v", len(got.Weeks), got.Weeks)
	}
	if got.Days != 3 {
		t.Errorf("want three distinct days, got %d", got.Days)
	}
	if err := runTests(dir, []string{"--verbose"}, &out); err == nil {
		t.Error("an unknown flag was accepted")
	}
	if err := runTests(dir, []string{"--limit"}, &out); err == nil {
		t.Error("--limit was accepted without a value")
	}
}

// Two screens that are easy to get wrong: a machine with no runs at all, and a
// list longer than the page.
func TestTestsScreenEdges(t *testing.T) {
	withStatsStores(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runTests(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no build or test runs") {
		t.Fatalf("a store with no runs does not say so:\n%s", out.String())
	}
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	var lines []string
	for i, name := range []string{"TestAlpha", "TestBeta"} {
		for d, day := range []string{"2026-04-01", "2026-04-02"} {
			lines = append(lines, testRunLines("r4", "/work/app", day, fmt.Sprintf("k%d%d", i, d),
				"go test ./internal/"+name,
				"--- FAIL: "+name+" (0.01s)\nFAIL\tgithub.com/me/app\t0.3s\nFAIL")...)
		}
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "r4.jsonl"), "r4", lines)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runTests(dir, []string{"--limit", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "1 more") {
		t.Errorf("the page does not say how many rows it held back:\n%s", got)
	}
	out.Reset()
	if err := runTests(dir, []string{"--limit", "0"}, &out); err != nil {
		t.Fatal(err)
	}
	if all := out.String(); !strings.Contains(all, "TestAlpha") || !strings.Contains(all, "TestBeta") {
		t.Errorf("--limit 0 did not print every row:\n%s", all)
	}
}
