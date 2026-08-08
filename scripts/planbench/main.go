// Command planbench runs a directory of plans through `deja check -` and
// reports how often the plan checker fires.
//
// It counts fires. It does not say whether a fire was right, and it cannot:
// precision on this path is a human label — someone who was there, reading the
// recalled wall against the plan in front of them — and both halves of that
// judgment are local. The hand-labelled set behind the 6/30 reported on #532
// came from private transcripts and will never ship, so the numbers in that
// thread are not reproducible by anyone else. This produces the denominator on
// a reader's own plans and a reader's own history, and leaves the verdict to
// the reader.
//
// It drives the built binary rather than importing the matcher, which is the
// one thing the other harnesses here do not do. `deja check -` is the only path
// that composes what actually fires: step extraction, the index lookup, the
// trust policy and the payload budget all live in package main beside it, and
// calling index.PlanFrictionMatches directly would measure a component no user
// runs — the same reason check exists at all (cmd/deja/check.go).
//
//	go build ./cmd/deja
//	DEJA_INDEX_DIR=~/.cache/deja/index.db go run ./scripts/planbench -plans ./plans [-deja ./deja] [-dump]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	plans := flag.String("plans", "", "directory of plan files, one plan per file")
	bin := flag.String("deja", "deja", "deja binary to run — a build of this tree, not whatever is installed")
	dump := flag.Bool("dump", false, "print each finding verbatim — recalled history text")
	flag.Parse()

	// No default: the only directory this tool could guess at is someone's own
	// plans, and a harness that reads a private directory when run bare is a
	// harness nobody can run bare.
	if *plans == "" {
		fmt.Fprintln(os.Stderr, "planbench needs -plans DIR")
		os.Exit(1)
	}
	// Looked up once, before any plan runs: "executable file not found" is a
	// setup error, and the same line repeated for every file in the directory
	// buries the one plan that failed for its own reasons.
	if _, err := exec.LookPath(*bin); err != nil {
		fmt.Fprintln(os.Stderr, "deja:", err)
		os.Exit(1)
	}
	if err := run(os.Stdout, os.Stderr, *bin, *plans, *dump); err != nil {
		fmt.Fprintln(os.Stderr, "planbench:", err)
		os.Exit(1)
	}
}

// newCheckCmd is a seam: a test drives a stand-in for deja without building one.
var newCheckCmd = func(bin string) *exec.Cmd {
	return exec.Command(bin, "check", "-")
}

type planResult struct {
	name     string
	findings []string
	// labels are the wall labels this plan cited, in the order the walls were
	// first seen across the whole run.
	labels []string
}

type wall struct {
	label string
	text  string
	plans int
}

func run(out, errOut io.Writer, bin, dir string, dump bool) error {
	names, err := planFiles(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "plans %s · %d file%s\n\n", dir, len(names), plural(len(names)))

	byWall := map[string]*wall{}
	var order []*wall
	var results []planResult
	failed := 0
	for _, name := range names {
		findings, err := check(bin, filepath.Join(dir, name))
		if err != nil {
			fmt.Fprintf(errOut, "%s: %v\n", name, err)
			failed++
			continue
		}
		r := planResult{name: name, findings: findings}
		for _, finding := range findings {
			text := planWall(finding)
			w := byWall[text]
			if w == nil {
				w = &wall{label: fmt.Sprintf("w%d", len(order)+1), text: text}
				byWall[text] = w
				order = append(order, w)
			}
			// planFindings emits a wall at most once per plan, so counting a
			// plan per finding here needs no de-duplication of its own.
			w.plans++
			r.labels = append(r.labels, w.label)
		}
		results = append(results, r)
	}

	printResults(out, results, dump)

	fired, findings := 0, 0
	for _, r := range results {
		if len(r.findings) > 0 {
			fired++
		}
		findings += len(r.findings)
	}
	fmt.Fprintf(out, "\n%d plan%s · %d fired (%.1f%%) · %d finding%s · %d distinct wall%s\n",
		len(results), plural(len(results)), fired, pct(fired, len(results)),
		findings, plural(findings), len(order), plural(len(order)))
	// The distinct-wall count is the number to read the finding count against.
	// Twelve findings drawn from two walls is one recurring problem reported
	// twelve times, not twelve independent hits, and the two shapes call for
	// different judgments about whether the feature earns its place.
	for _, w := range order {
		fmt.Fprintf(out, "  %-4s cited by %d plan%s\n", w.label, w.plans, plural(w.plans))
	}
	if failed > 0 {
		// A plan that fired nothing is a result — silence is the miss contract
		// of the hook this measures, so it exits 0 and the tool stays usable in
		// a loop. A plan that could not be run is not a result.
		return fmt.Errorf("%d plan%s could not be run", failed, plural(failed))
	}
	return nil
}

func printResults(out io.Writer, results []planResult, dump bool) {
	width := 12
	for _, r := range results {
		if n := len(r.name); n > width {
			width = n
		}
	}
	if width > 40 {
		width = 40
	}
	for _, r := range results {
		if len(r.findings) == 0 {
			fmt.Fprintf(out, "  %-*s  silent\n", width, r.name)
			continue
		}
		fmt.Fprintf(out, "  %-*s  fire · %d finding%s · %s\n",
			width, r.name, len(r.findings), plural(len(r.findings)), strings.Join(r.labels, " "))
		// Findings quote a stranger's history back at whoever is reading the
		// table, which is fine on their own machine and not fine in a pasted
		// result. Counts and labels by default, text on request — the rule
		// corpusprobe follows for the same reason.
		if !dump {
			continue
		}
		for i, finding := range r.findings {
			fmt.Fprintf(out, "  %-*s  %-4s %s\n", width, "", r.labels[i], finding)
		}
	}
}

// planFiles lists the plans to run. Every regular file counts: plans are
// Markdown in practice, but nothing in the checker reads an extension, and a
// harness that filtered on one would report a smaller denominator than the
// directory holds. Dot files are skipped so a stray .DS_Store is not a plan.
func planFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		// Reporting 0/0 for a mistyped directory is the one failure this tool
		// could hide from a loop that only reads the summary.
		return nil, fmt.Errorf("no plan files in %s", dir)
	}
	sort.Strings(names)
	return names, nil
}

// findingPrefix opens every line formatPlanFinding writes
// (cmd/deja/hook_plan.go). It is the harness's one coupling to the finding
// text, and it is deliberate: a line that does not start with it did not come
// from the plan checker.
const findingPrefix = "wall hit in "

func check(bin, path string) ([]string, error) {
	plan, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cmd := newCheckCmd(bin)
	cmd.Stdin = bytes.NewReader(plan)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	var findings []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, findingPrefix) {
			// The first real run of this harness measured `deja search`. A deja
			// older than the check subcommand does not fail on `deja check -`:
			// an unknown first word falls through to search (cmd/deja/main.go),
			// which ignores stdin, exits 0, and answers every plan with the
			// same result lines — 47 findings for a one-step plan, and the same
			// 47 for the next plan. Every finding starts with this prefix
			// (formatPlanFinding), truncation takes the tail, so a line that
			// does not is not a finding and the count is not a measurement.
			//
			// The line itself is not quoted here: it is whatever that other
			// command decided to print out of a private history.
			return nil, fmt.Errorf("printed a line that is not a finding — does %s have the check subcommand from this tree?", bin)
		}
		findings = append(findings, line)
	}
	return findings, nil
}

// planWall extracts the wall a finding cites, so the same wall recalled by
// several plans collapses to one label. formatPlanFinding writes
// `wall hit in N sessions[ since date]: "wall"` and appends the command clause
// after a semicolon, so the wall is the first quoted string on the line.
//
// A finding trimmed to fit the payload budget can lose its closing quote. Then
// the whole line is the key, and two truncated findings differing only past the
// cut count as two walls — this overstates how many distinct walls fired rather
// than collapsing walls that are not the same.
func planWall(line string) string {
	i := strings.Index(line, `"`)
	if i < 0 {
		return line
	}
	for j := i + 1; j < len(line); j++ {
		switch line[j] {
		case '\\':
			j++
		case '"':
			if text, err := strconv.Unquote(line[i : j+1]); err == nil {
				return text
			}
			return line
		}
	}
	return line
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
