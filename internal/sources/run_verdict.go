package sources

import "regexp"

// The verdict lines a test or build runner prints. A runner says how it went
// in a shape it owns, and that shape is what gets read — never the body of the
// output.
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
)

// RunOutputVerdict reads a runner's own verdict out of the output a run left
// behind: "failed", "passed", or "" when the output has no verdict line. A
// compiler error counts as a failure: the suite did not run, which is not a
// pass by any reading.
//
// The order matters. `go build` prints nothing on success, so an agent's own
// `echo ok` in the same pipeline sits above a compile error in the same output
// — the failure has to win.
func RunOutputVerdict(out string) string {
	switch {
	case goFailRE.MatchString(out), pyFailRE.MatchString(out), jsFailRE.MatchString(out),
		rustFailRE.MatchString(out), compileFail.MatchString(out):
		return "failed"
	case goPassRE.MatchString(out), pyPassRE.MatchString(out), jsPassRE.MatchString(out),
		rustPassRE.MatchString(out), wrapPassRE.MatchString(out):
		return "passed"
	}
	return ""
}
