package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelperDeja is not a test. It is the stand-in for the deja binary: it
// reads a plan on stdin and prints one finding per line marked FIRE, which is
// all planbench needs from it, and it exits on EXIT so the failure path can be
// exercised without a broken install.
func TestHelperDeja(t *testing.T) {
	if os.Getenv("PLANBENCH_HELPER") != "1" {
		return
	}
	// Before the test framework writes PASS to the stdout planbench is reading.
	defer os.Exit(0)
	plan, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	for _, line := range strings.Split(string(plan), "\n") {
		line = strings.TrimSpace(line)
		if line == "EXIT" {
			fmt.Fprintln(os.Stderr, "deja: check failed")
			os.Exit(1)
		}
		// A deja older than the check subcommand searches for the words
		// instead, ignores stdin, and exits 0 with result lines.
		if line == "OLD" {
			fmt.Println("[claude · a-project · 2026-01-02 · 019fa282] some session title")
			os.Exit(0)
		}
		if finding, ok := strings.CutPrefix(line, "FIRE "); ok {
			fmt.Println(finding)
		}
	}
}

// useHelper points the harness at the stand-in above.
func useHelper(t *testing.T) {
	t.Helper()
	original := newCheckCmd
	newCheckCmd = func(string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperDeja")
		cmd.Env = append(os.Environ(), "PLANBENCH_HELPER=1")
		return cmd
	}
	t.Cleanup(func() { newCheckCmd = original })
}

// unpad collapses the column padding so a test asserts on what a line says
// rather than on how wide the longest file name happened to be.
func unpad(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.Join(lines, "\n")
}

func writePlans(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const (
	migrationWall = `wall hit in 3 sessions since 2026-01-02: "the migration lock never released"`
	timeoutWall   = `wall hit in 2 sessions since 2026-01-05: "the pool timed out"; past sessions also ran "make reset" (2 sessions)`
)

// The finding count alone reads as independent hits. Twelve findings drawn from
// two walls is one problem reported twelve times, so the distinct-wall count is
// the number that decides how to read the rest of the table — and it only works
// if the same wall reached through different plans collapses to one label.
func TestRunCountsDistinctWallsNotFindings(t *testing.T) {
	useHelper(t)
	dir := writePlans(t, map[string]string{
		"a.md": "1. do the thing\nFIRE " + migrationWall + "\nFIRE " + timeoutWall + "\n",
		"b.md": "1. do another thing\nFIRE " + migrationWall + "\n",
		"c.md": "1. nothing recurs here\n",
	})

	var out, errOut bytes.Buffer
	if err := run(&out, &errOut, "deja", dir, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := unpad(out.String())

	for _, want := range []string{
		"a.md fire · 2 findings · w1 w2",
		"b.md fire · 1 finding · w1",
		"c.md silent",
		"3 plans · 2 fired (66.7%) · 3 findings · 2 distinct walls",
		"w1 cited by 2 plans",
		"w2 cited by 1 plan",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
}

// A finding quotes someone's history back at whoever reads the table, and the
// table is the part that gets pasted into an issue. Text only on request.
func TestRunPrintsHistoryTextOnlyWithDump(t *testing.T) {
	useHelper(t)
	dir := writePlans(t, map[string]string{
		"a.md": "1. do the thing\nFIRE " + migrationWall + "\n",
	})

	var quiet, dumped, errOut bytes.Buffer
	if err := run(&quiet, &errOut, "deja", dir, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(quiet.String(), "the migration lock never released") {
		t.Errorf("recalled text printed without -dump\n%s", quiet.String())
	}
	if err := run(&dumped, &errOut, "deja", dir, true); err != nil {
		t.Fatalf("run -dump: %v", err)
	}
	if !strings.Contains(dumped.String(), "the migration lock never released") {
		t.Errorf("-dump printed no finding text\n%s", dumped.String())
	}
}

// Silence is the miss contract of the hook this measures, so a run where
// nothing fires has to exit 0 or the tool cannot be used in a loop. A plan that
// could not be run is a different thing, and it must not take the rest of the
// directory with it.
func TestRunSeparatesSilenceFromFailure(t *testing.T) {
	useHelper(t)
	silent := writePlans(t, map[string]string{
		"a.md": "1. nothing recurs here\n",
		"b.md": "1. nor here\n",
	})
	var out, errOut bytes.Buffer
	if err := run(&out, &errOut, "deja", silent, false); err != nil {
		t.Fatalf("a silent run is a result, not an error: %v", err)
	}
	if !strings.Contains(out.String(), "2 plans · 0 fired (0.0%) · 0 findings · 0 distinct walls") {
		t.Errorf("unexpected summary\n%s", out.String())
	}

	broken := writePlans(t, map[string]string{
		"a.md": "1. nothing recurs here\n",
		"b.md": "EXIT\n",
		"c.md": "1. still measured\nFIRE " + migrationWall + "\n",
	})
	out.Reset()
	errOut.Reset()
	err := run(&out, &errOut, "deja", broken, false)
	if err == nil {
		t.Fatal("a plan that could not be run exited 0")
	}
	if !strings.Contains(err.Error(), "1 plan could not be run") {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(errOut.String(), "b.md:") {
		t.Errorf("the failing plan was not named: %q", errOut.String())
	}
	// The two plans either side of the failure still count.
	if !strings.Contains(out.String(), "2 plans · 1 fired (50.0%) · 1 finding · 1 distinct wall") {
		t.Errorf("a failed plan changed the denominator\n%s", out.String())
	}
}

// The first real run of this harness measured `deja search`: an installed deja
// with no check subcommand falls through to search, ignores the plan on stdin,
// exits 0, and answers every plan with the same result lines. That reads as a
// fire on every plan, and a one-step plan came back with 47 findings. A run
// against the wrong binary has to fail, not report a rate.
func TestRunRefusesOutputThatIsNotAFinding(t *testing.T) {
	useHelper(t)
	dir := writePlans(t, map[string]string{"a.md": "OLD\n"})

	var out, errOut bytes.Buffer
	err := run(&out, &errOut, "deja", dir, false)
	if err == nil {
		t.Fatal("search results were counted as findings")
	}
	if !strings.Contains(errOut.String(), "not a finding") {
		t.Errorf("stderr = %q", errOut.String())
	}
	// Whatever that other command printed came out of a private history, and
	// this table gets pasted into issues.
	if strings.Contains(errOut.String()+out.String(), "019fa282") {
		t.Errorf("the rejected line was echoed\n%s%s", errOut.String(), out.String())
	}
}

// A mistyped -plans is the one failure a loop reading only the summary would
// never see: 0/0 looks exactly like a directory where nothing fired.
func TestPlanFilesRefusesADirectoryWithNoPlans(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := planFiles(dir); err == nil {
		t.Fatal("a directory holding no plan accepted")
	}
	if _, err := planFiles(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a directory that does not exist accepted")
	}

	if err := os.WriteFile(filepath.Join(dir, "plan.txt"), []byte("1. step"), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := planFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Extension-agnostic on purpose: nothing in the checker reads one.
	if len(names) != 1 || names[0] != "plan.txt" {
		t.Errorf("names = %v", names)
	}
}

func TestPlanWallReadsTheWallOutOfAFinding(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "wall alone",
			line: migrationWall,
			want: "the migration lock never released",
		},
		{
			// The command clause is a bonus on the same line, and grouping by
			// it would split one wall across the plans that also recalled a
			// command.
			name: "command clause ignored",
			line: timeoutWall,
			want: "the pool timed out",
		},
		{
			name: "quote inside the wall",
			line: `wall hit in 2 sessions: "go test said \"FAIL\" again"`,
			want: `go test said "FAIL" again`,
		},
		{
			// Truncated by the payload budget: no closing quote to unquote.
			name: "no closing quote",
			line: `wall hit in 2 sessions: "the migration lock never rele…`,
			want: `wall hit in 2 sessions: "the migration lock never rele…`,
		},
		{
			name: "no quote at all",
			line: "wall hit in 2 sessions",
			want: "wall hit in 2 sessions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planWall(tt.line); got != tt.want {
				t.Errorf("planWall(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}
