package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
)

// `deja tests` is the build and test history already in the transcripts (#539).
//
// Nothing new is captured for it. Every agent records the command it ran and
// the output that came back, so ten months of runs are sitting in the store —
// on this machine 12,897 of them across 107 days. What the corpus does not
// hold is an exit code: three harnesses append one, which covers 2% of runs.
//
// So the screen shows three bands, not two. A run counts as failed or passed
// only when the runner's own verdict line says so, and the third band is the
// runs whose output was filtered before it was recorded — `grep -E '^FAIL'`
// with nothing to print, and an `echo DONE` after it. On this store that is
// 43% of runs, and calling them passes would turn a 2-in-5 failure rate into
// 1-in-5. See index.TestRunVerdict for what was measured by hand.

// testsWeeks is how many weeks of the series the screen shows. The JSON holds
// every week.
const testsWeeks = 8

// testsDefaultLimit is how many repeat-failing tests the screen names.
const testsDefaultLimit = 8

type testsJSON struct {
	Kind      string             `json:"kind"`
	Schema    int                `json:"schema_version"`
	Runs      int                `json:"runs"`
	Failed    int                `json:"failed"`
	Passed    int                `json:"passed"`
	NoVerdict int                `json:"no_verdict"`
	Days      int                `json:"days"`
	First     string             `json:"first,omitempty"`
	Last      string             `json:"last,omitempty"`
	Tools     map[string]int     `json:"tools,omitempty"`
	Weeks     []index.TestWeek   `json:"weeks"`
	Repeats   []index.TestRepeat `json:"repeat_failures"`
	Withheld  int                `json:"withheld,omitempty"`
}

const testsJSONKind = "deja.tests"

func runTests(dir string, args []string, stdout io.Writer) error {
	asJSON := false
	limit := testsDefaultLimit
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--limit":
			if i+1 >= len(args) {
				return fmt.Errorf("tests: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return fmt.Errorf("tests: --limit wants a number, got %q", args[i])
			}
			limit = n
		default:
			return fmt.Errorf("tests: unknown flag %q — it takes --limit n and --json", args[i])
		}
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	h, err := index.ScanTestHistory(dir)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		out := testsJSON{
			Kind: testsJSONKind, Schema: jsonout.Version,
			Runs: h.Runs, Failed: h.Failed, Passed: h.Passed, NoVerdict: h.NoVerdict,
			Days: h.Days, Tools: h.Tools, Weeks: h.Weeks, Repeats: h.Repeats,
			Withheld: h.Withheld,
		}
		if out.Weeks == nil {
			out.Weeks = []index.TestWeek{}
		}
		if out.Repeats == nil {
			out.Repeats = []index.TestRepeat{}
		}
		if !h.First.IsZero() {
			out.First = h.First.Local().Format("2006-01-02")
			out.Last = h.Last.Local().Format("2006-01-02")
		}
		return enc.Encode(out)
	}
	printTests(stdout, h, limit)
	return nil
}

func printTests(w io.Writer, h index.TestHistory, limit int) {
	if h.Runs == 0 {
		fmt.Fprintln(w, "no build or test runs in your agent history")
		if h.Withheld > 0 {
			fmt.Fprintf(w, "the ignore rule kept %d session%s out of this scan\n", h.Withheld, pluralS(h.Withheld))
		}
		return
	}
	fmt.Fprintf(w, "%d build and test run%s on %d day%s, %s to %s\n",
		h.Runs, pluralS(h.Runs), h.Days, pluralS(h.Days),
		h.First.Local().Format("2006-01-02"), h.Last.Local().Format("2006-01-02"))
	weeks := h.Weeks
	if len(weeks) > testsWeeks {
		weeks = weeks[len(weeks)-testsWeeks:]
	}
	for _, k := range weeks {
		fmt.Fprintf(w, "\n  week of %s  %4d run%s  %4d failed  %4d passed", k.Start.Format("2006-01-02"),
			k.Runs, pluralS(k.Runs), k.Failed, k.Passed)
		if k.NoVerdict > 0 {
			fmt.Fprintf(w, "  %4d no verdict", k.NoVerdict)
		}
	}
	fmt.Fprintln(w)
	if s := testsToolLine(h.Tools); s != "" {
		fmt.Fprintf(w, "\n%s\n", s)
	}
	// The rate, stated over the runs it was actually measured on. Over every
	// run it would be a third smaller, and the difference is the band below,
	// not a difference in how often anything failed.
	if answered := h.Failed + h.Passed; answered > 0 {
		fmt.Fprintf(w, "\n%d of the %d runs that carried a verdict failed (%d%%)\n",
			h.Failed, answered, percentOf(h.Failed, answered))
	}
	if h.NoVerdict > 0 {
		// Said plainly, because the number is large and the reason for it is
		// not a gap in the store: the agent piped its own run through a filter
		// that printed nothing, so the verdict never reached the transcript.
		fmt.Fprintf(w, "\nthe verdict is the runner's own line — %d run%s (%d%%) had it filtered out of the pipeline before the output was recorded, and are counted as neither\n",
			h.NoVerdict, pluralS(h.NoVerdict), percentOf(h.NoVerdict, h.Runs))
	}
	printTestRepeats(w, h, limit)
	if h.Withheld > 0 {
		fmt.Fprintf(w, "the ignore rule kept %d session%s out of this scan\n", h.Withheld, pluralS(h.Withheld))
	}
}

// printTestRepeats names the tests that failed on more than one day. A test
// that failed twenty times in one sitting was being fixed; one that failed on
// nine separate days is the one worth knowing about before touching the file
// it guards.
func printTestRepeats(w io.Writer, h index.TestHistory, limit int) {
	if len(h.Repeats) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%d test%s failed on more than one day\n", len(h.Repeats), pluralS(len(h.Repeats)))
	shown := h.Repeats
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, r := range shown {
		fmt.Fprintf(w, "  %-52s %d failure%s on %d days, last %s\n", cutName(r.Name, 52),
			r.Failures, pluralS(r.Failures), r.Days, r.LastFail.Local().Format("2006-01-02"))
	}
	if n := len(h.Repeats) - len(shown); n > 0 {
		fmt.Fprintf(w, "  %d more — `deja tests --limit 0` for all of them, `--json` for the series too\n", n)
	}
}

func cutName(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func percentOf(part, whole int) int {
	if whole == 0 {
		return 0
	}
	return int(float64(part)/float64(whole)*100 + 0.5)
}

// testsToolLine names the runners behind the series, biggest first, so the
// count can be checked against what someone knows they run.
func testsToolLine(tools map[string]int) string {
	type row struct {
		name string
		n    int
	}
	rows := make([]row, 0, len(tools))
	for k, v := range tools {
		rows = append(rows, row{k, v})
	}
	if len(rows) == 0 {
		return ""
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].name < rows[j].name
	})
	if len(rows) > 5 {
		rows = rows[:5]
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s %d", r.name, r.n))
	}
	return strings.Join(parts, ", ")
}
