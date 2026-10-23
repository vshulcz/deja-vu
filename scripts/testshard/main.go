// Command testshard prints the -run and -skip patterns that give one CI shard
// its slice of the test suite.
//
// cmd/deja alone is over three thousand tests in one binary, and a package's
// tests run one after another, so the test job was as long as that package no
// matter how many cores the runner had. Splitting it means splitting by test
// name, and a list of a thousand names in -run is longer than a windows
// command line. So a shard is a range of names instead: every test, fuzz
// target and example in the repository is sorted, cut into N runs of equal
// count, and shard i gets the names from its own first name up to the next
// shard's first name — `-run` matches "at or after the start", `-skip` matches
// "at or after the end". Each pattern is a couple of kilobytes.
//
// The ranges cover every possible name, not just the ones found here, so a test
// this scanner misses still runs in exactly one shard. The cuts fall at equal
// time rather than equal count, from weights.txt: what each test took in one
// `go test -race -json` run, since the legs that wait longest run with -race.
// A stale file only moves the cuts; it cannot drop a test.
//
//	go run ./scripts/testshard -shard 2 -of 3 >> "$GITHUB_OUTPUT"
//	go test -run "$RUN" -skip "$SKIP" ./...
//
// To refresh the weights:
//
//	go test -race -count=1 -json ./... | go run ./scripts/testshard -record > scripts/testshard/weights.txt
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// weightsFile is where the recorded times live, relative to the root.
const weightsFile = "scripts/testshard/weights.txt"

func main() {
	shard := flag.Int("shard", 1, "which shard, counting from 1")
	of := flag.Int("of", 1, "how many shards")
	root := flag.String("root", ".", "repository root")
	rec := flag.Bool("record", false, "read go test -json on stdin and print a weights file")
	flag.Parse()
	if *rec {
		if err := record(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "testshard:", err)
			os.Exit(1)
		}
		return
	}
	if *of < 1 || *shard < 1 || *shard > *of {
		fmt.Fprintf(os.Stderr, "testshard: -shard %d -of %d is not a shard\n", *shard, *of)
		os.Exit(2)
	}
	names, err := testNames(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testshard:", err)
		os.Exit(1)
	}
	w, err := readWeights(filepath.Join(*root, weightsFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "testshard:", err)
		os.Exit(1)
	}
	run, skip := patterns(names, w.of, *shard, *of)
	fmt.Printf("run=%s\nskip=%s\n", run, skip)
}

// weights is what each test took. A test not listed costs def.
type weights struct {
	byName map[string]float64
	def    float64
}

func (w weights) of(name string) float64 {
	if v, ok := w.byName[name]; ok {
		return v
	}
	return w.def
}

// readWeights reads "default <seconds>" and then "<name> <seconds>" lines. No
// file means every test weighs the same.
func readWeights(path string) (weights, error) {
	w := weights{byName: map[string]float64{}, def: 1}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return w, nil
	}
	if err != nil {
		return w, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) != 2 {
			return w, fmt.Errorf("%s:%d: want a name and seconds", path, line)
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || v < 0 {
			return w, fmt.Errorf("%s:%d: %q is not a duration in seconds", path, line, fields[1])
		}
		if fields[0] == "default" {
			w.def = v
		} else {
			w.byName[fields[0]] = v
		}
	}
	return w, sc.Err()
}

// listedFrom is the least a test has to take to get a line of its own; the
// rest share the default, which keeps the file under a thousand lines.
const listedFrom = 0.1

// record turns a go test -json stream into a weights file. It keeps only the
// slowest package's times: packages run side by side, so a shard lasts about
// as long as its share of that one, and cmd/deja is most of the suite.
func record(in io.Reader, out io.Writer) error {
	byPkg := map[string]map[string]float64{}
	sums := map[string]float64{}
	dec := json.NewDecoder(in)
	for {
		var e struct {
			Action  string  `json:"Action"`
			Package string  `json:"Package"`
			Test    string  `json:"Test"`
			Elapsed float64 `json:"Elapsed"`
		}
		if err := dec.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if e.Test == "" || strings.Contains(e.Test, "/") {
			continue
		}
		if e.Action == "pass" || e.Action == "fail" || e.Action == "skip" {
			if byPkg[e.Package] == nil {
				byPkg[e.Package] = map[string]float64{}
			}
			byPkg[e.Package][e.Test] += e.Elapsed
			sums[e.Package] += e.Elapsed
		}
	}
	if len(byPkg) == 0 {
		return errors.New("no test results on stdin; pipe in go test -json")
	}
	slowest := ""
	for pkg, sum := range sums {
		if slowest == "" || sum > sums[slowest] || sum == sums[slowest] && pkg < slowest {
			slowest = pkg
		}
	}
	took := byPkg[slowest]
	var listed []string
	var rest float64
	var restN int
	for name, v := range took {
		if v >= listedFrom {
			listed = append(listed, name)
		} else {
			rest += v
			restN++
		}
	}
	sort.Strings(listed)
	def := 0.01
	if restN > 0 && rest > 0 {
		def = rest / float64(restN)
	}
	fmt.Fprintln(out, "# go test -race -count=1 -json ./... | go run ./scripts/testshard -record")
	fmt.Fprintf(out, "# %s, %.0fs\n", slowest, sums[slowest])
	fmt.Fprintf(out, "default %.4f\n", def)
	for _, name := range listed {
		fmt.Fprintf(out, "%s %.2f\n", name, took[name])
	}
	return nil
}

// topLevel is a top-level test, fuzz target or example. A method cannot match:
// its receiver puts a parenthesis right after func.
var topLevel = regexp.MustCompile(`(?m)^func ((?:Test|Fuzz|Example)[^\s(\[]*)[(\[]`)

// testNames returns every distinct test function name in the module's
// _test.go files, sorted. Build constraints are ignored on purpose: every
// runner must cut at the same names whatever its GOOS.
func testNames(root string) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // walks the repository's own tree
		if err != nil {
			return err
		}
		for _, m := range topLevel.FindAllSubmatch(src, -1) {
			seen[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// patterns returns the -run and -skip values for shard i of n over the sorted
// names. An empty value is go test's own default: run everything, skip nothing.
func patterns(names []string, weight func(string) float64, i, n int) (run, skip string) {
	if len(names) < n {
		// Too few to cut: the first shard takes everything.
		if i == 1 {
			return "", ""
		}
		return "^$", ""
	}
	starts := cuts(names, weight, n)
	if i > 1 {
		run = atOrAfter(names[starts[i-1]])
	}
	if i < n {
		skip = atOrAfter(names[starts[i]])
	}
	return run, skip
}

// cuts returns where each of n shards starts in names, so that each holds
// about the same weight and none is empty. len(names) must be at least n.
func cuts(names []string, weight func(string) float64, n int) []int {
	var total float64
	for _, name := range names {
		total += weight(name)
	}
	starts := make([]int, n)
	k, cum := 1, 0.0
	for idx, name := range names {
		for k < n && cum >= total*float64(k)/float64(n) {
			starts[k] = idx
			k++
		}
		cum += weight(name)
	}
	for ; k < n; k++ {
		starts[k] = len(names)
	}
	// Every shard gets at least one name, and the cuts stay in order.
	for k := 1; k < n; k++ {
		starts[k] = min(max(starts[k], starts[k-1]+1), len(names)-(n-k))
	}
	return starts
}

// atOrAfter returns a pattern matching exactly the names that sort at or after
// p, in Go's byte order (which for UTF-8 is code point order). At each rune the
// name either carries on with p's rune, or has a greater one there and anything
// after it; having all of p is enough.
func atOrAfter(p string) string {
	return "^" + rest([]rune(p))
}

func rest(p []rune) string {
	if len(p) == 0 {
		return ""
	}
	r := p[0]
	alt := regexp.QuoteMeta(string(r)) + rest(p[1:])
	if r < utf8.MaxRune {
		alt += fmt.Sprintf(`|[\x{%x}-\x{10ffff}]`, r+1)
	}
	return "(?:" + alt + ")"
}
