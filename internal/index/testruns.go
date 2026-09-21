package index

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// The build and test history a transcript already holds (#539).
//
// Two things had to be measured before any of this was worth writing. The
// first: how much of it is there. On a 2,700-session store, 18,645 build and
// test runs across 107 separate days — ten months of them, without a hook, a
// wrapper or any new capture path.
//
// The second: whether the corpus says how each one ended. Only three harnesses
// append an exit code to the command line, which covers 2%. The output record
// that follows covers the rest, but reading it with `LooksLikeError` — the
// predicate the fix-pair miner uses — is wrong here in both directions. Hand
// read, 25 of its failures were 22 real ones, two runs that only skipped a test
// and one that passed while printing fixture text full of error strings; 25 of
// its passes hid two real failures, a `go vet` type error and a run that ended
// in `FAIL\tpkg\t0.7s`. It was tuned for "did this error come back", not for
// "did the suite pass".
//
// So the verdict here is the runner's own line and nothing else, and when there
// is no such line the run is counted as having no verdict rather than as a
// pass. That band is 30% of runs on this store, and almost all of it is one
// thing: the agent piped its own run through `grep -E '^FAIL'` and printed
// `echo DONE`. A number that quietly called those passes would put the failure
// rate at 27%; over the runs that answered it is 38%.

// TestVerdict is what a run's own output said about it.
type TestVerdict string

const (
	TestPassed    TestVerdict = "passed"
	TestFailed    TestVerdict = "failed"
	TestNoVerdict TestVerdict = "no verdict"
)

// buildTestRE matches the command lines this counts as a build or test run. It
// is deliberately a list of runners rather than a guess: `go test`, not any
// command with "test" in it, which would catch every path holding a
// _test.go file.
var buildTestRE = regexp.MustCompile(`(^|[;&|(]\s*|\s)(go test|go build|go vet|make test|make build|make check|npm test|npm run test|yarn test|pnpm test|pytest|python -m pytest|cargo test|cargo build|cargo check|jest|vitest|tox|docker build|docker compose build|gradle test|mvn test|dotnet test|ctest|bun test|swift test)\b`)

// The verdict lines themselves. A runner says how it went in a shape it owns,
// and that shape is what gets read — never the body of the output.
var (
	goFailRE = regexp.MustCompile(`(?m)^(--- FAIL: |FAIL[ \t]|FAIL$|# \S+ \[build failed\]|vet: )`)
	// `ok` has to carry what `go test` prints after it — the package and either
	// an elapsed time or `(cached)`. Accepting the bare word read `ok main`
	// from a `git push` and `ok  friends.sh` from a shell script audit as
	// passing test runs, two of sixteen in a hand-read sample.
	goPassRE = regexp.MustCompile(`(?m)(^ok[ \t]+\S+[ \t]+(\(cached\)|[0-9.]+m?s)|^PASS$|^--- PASS: |^\?[ \t]+\S+[ \t]+\[no test files\])`)
	pyFailRE = regexp.MustCompile(`(?m)^(=+ .*\b\d+ (failed|error|errors)\b|FAILED \S+)`)
	pyPassRE = regexp.MustCompile(`(?m)^=+ .*\b\d+ passed\b`)
	jsFailRE = regexp.MustCompile(`(?m)^(Tests:\s+\d+ failed|Test Suites:\s+\d+ failed)`)
	jsPassRE = regexp.MustCompile(`(?m)^(Tests:\s+.*\d+ passed|Test Suites:\s+.*\d+ passed)`)
	// Cargo's own shapes, not a bare `error:` — that one read `error: No valid
	// patches in input` from a `git apply` as a failing test run.
	rustFailRE  = regexp.MustCompile(`(?m)^(error\[E\d+\]: |error: (could not compile|test failed)|test result: FAILED)`)
	rustPassRE  = regexp.MustCompile(`(?m)^test result: ok\.`)
	wrapPassRE  = regexp.MustCompile(`(?m)^Go test: \d+ passed`)
	compileFail = regexp.MustCompile(`(?m)^\S+\.(go|py|ts|tsx|js|rs|java|kt|swift):\d+:\d+: `)
	namedFailRE = regexp.MustCompile(`(?m)^\s*--- FAIL: ([A-Za-z0-9_]+)`)
)

// TestRunVerdict reads a runner's own verdict out of the output a run left
// behind. A compiler error counts as a failure: the suite did not run, which is
// not a pass by any reading.
//
// The order matters. `go build` prints nothing on success, so an agent's own
// `echo ok` in the same pipeline sits above a compile error in the same output
// — the failure has to win.
func TestRunVerdict(out string) TestVerdict {
	switch {
	case goFailRE.MatchString(out), pyFailRE.MatchString(out), jsFailRE.MatchString(out),
		rustFailRE.MatchString(out), compileFail.MatchString(out):
		return TestFailed
	case goPassRE.MatchString(out), pyPassRE.MatchString(out), jsPassRE.MatchString(out),
		rustPassRE.MatchString(out), wrapPassRE.MatchString(out):
		return TestPassed
	}
	return TestNoVerdict
}

// TestWeek is one week of runs, by what their output said.
type TestWeek struct {
	Start     time.Time `json:"start"`
	Runs      int       `json:"runs"`
	Failed    int       `json:"failed"`
	Passed    int       `json:"passed"`
	NoVerdict int       `json:"no_verdict"`
}

// TestRepeat is a named test that failed on more than one day. The count of
// days is the number that matters: twelve failures in one afternoon is one
// thing being fixed, twelve failures on twelve days is a test that keeps
// coming back.
type TestRepeat struct {
	Name     string    `json:"name"`
	Project  string    `json:"project,omitempty"`
	Failures int       `json:"failures"`
	Days     int       `json:"days"`
	LastFail time.Time `json:"last_failure"`
}

// TestHistory is the series and the repeat offenders from one pass.
type TestHistory struct {
	Runs      int
	Failed    int
	Passed    int
	NoVerdict int
	Days      int
	First     time.Time
	Last      time.Time
	Tools     map[string]int
	Weeks     []TestWeek
	Repeats   []TestRepeat
	Withheld  int
}

// ScanTestHistory reads every command and tool-output record once and returns
// the build and test history in it.
func ScanTestHistory(dir string) (TestHistory, error) {
	out := TestHistory{Tools: map[string]int{}}
	type pending struct {
		tool    string
		when    time.Time
		counted bool
	}
	open := map[string]pending{}
	pol := policy.Load()
	days := map[string]bool{}
	weeks := map[time.Time]*TestWeek{}
	type repeat struct {
		project  string
		failures int
		days     map[string]bool
		last     time.Time
	}
	repeats := map[string]*repeat{}
	withheld := map[string]bool{}
	count := func(when time.Time, v TestVerdict) {
		out.Runs++
		days[when.Format("2006-01-02")] = true
		if out.First.IsZero() || when.Before(out.First) {
			out.First = when
		}
		if when.After(out.Last) {
			out.Last = when
		}
		w := weekStart(when)
		b := weeks[w]
		if b == nil {
			b = &TestWeek{Start: w}
			weeks[w] = b
		}
		b.Runs++
		switch v {
		case TestFailed:
			out.Failed++
			b.Failed++
		case TestPassed:
			out.Passed++
			b.Passed++
		default:
			out.NoVerdict++
			b.NoVerdict++
		}
	}
	err := eachRecordOfRoles(dir, map[string]bool{roleCommand: true, roleToolOutput: true},
		func(meta SessionMeta, r Record) {
			if pol.Ignored(meta.Path, meta.Project) {
				withheld[r.Key] = true
				return
			}
			if r.Role == roleCommand {
				for _, line := range strings.Split(r.Text, "\n") {
					m := buildTestRE.FindStringSubmatch(line)
					if m == nil {
						continue
					}
					// A command whose output never reached the transcript still
					// ran, so it is counted when the next one replaces it.
					if p, ok := open[r.Key]; ok && !p.counted {
						count(p.when, TestNoVerdict)
					}
					// One run per record: a command that builds and then tests
					// leaves one output, so counting both halves would double
					// every invocation in the series.
					open[r.Key] = pending{tool: m[2], when: r.Time}
					out.Tools[m[2]]++
					break
				}
				return
			}
			// Named failures are mined from every output, not only from the
			// ones that answer a command deja matched: `make check` and a shell
			// script both print `--- FAIL:` lines, and the test named in them
			// is the same test.
			for _, m := range namedFailRE.FindAllStringSubmatch(r.Text, -1) {
				s := repeats[m[1]]
				if s == nil {
					s = &repeat{project: meta.Project, days: map[string]bool{}}
					repeats[m[1]] = s
				}
				s.failures++
				s.days[r.Time.Format("2006-01-02")] = true
				if r.Time.After(s.last) {
					s.last = r.Time
				}
			}
			p, ok := open[r.Key]
			if !ok {
				return
			}
			v := TestRunVerdict(r.Text)
			// The first output after the command is that command's run. Later
			// ones count only when they carry a verdict of their own, and the
			// reason is a property of the store rather than a guess: a session
			// that runs the identical command line twice keeps one command
			// record and both outputs, so without this every repeated `go test
			// ./...` is one run in the series. Counting every following output
			// instead would count the next file read as a test run.
			if p.counted && v == TestNoVerdict {
				return
			}
			p.counted = true
			open[r.Key] = p
			count(p.when, v)
		})
	if err != nil {
		return TestHistory{}, err
	}
	// A command whose output never made it into the transcript still ran.
	for _, p := range open {
		if !p.counted {
			count(p.when, TestNoVerdict)
		}
	}
	out.Days = len(days)
	out.Withheld = len(withheld)
	for _, w := range weeks {
		out.Weeks = append(out.Weeks, *w)
	}
	sort.Slice(out.Weeks, func(i, j int) bool { return out.Weeks[i].Start.Before(out.Weeks[j].Start) })
	for name, s := range repeats {
		if len(s.days) < 2 {
			continue
		}
		out.Repeats = append(out.Repeats, TestRepeat{
			Name: name, Project: s.project, Failures: s.failures,
			Days: len(s.days), LastFail: s.last,
		})
	}
	sort.Slice(out.Repeats, func(i, j int) bool {
		a, b := out.Repeats[i], out.Repeats[j]
		if a.Days != b.Days {
			return a.Days > b.Days
		}
		if a.Failures != b.Failures {
			return a.Failures > b.Failures
		}
		return a.Name < b.Name
	})
	return out, nil
}

// weekStart is the Monday of a time's week, in local time, because a week of
// work is read in the timezone it was done in.
func weekStart(t time.Time) time.Time {
	t = t.Local()
	back := (int(t.Weekday()) + 6) % 7
	y, m, d := t.AddDate(0, 0, -back).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
