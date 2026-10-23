package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// shard is how go test reads the pair: run a test if -run matches (or is
// empty) and -skip does not.
type shard struct{ run, skip *regexp.Regexp }

func compile(run, skip string) shard {
	var s shard
	if run != "" {
		s.run = regexp.MustCompile(run)
	}
	if skip != "" {
		s.skip = regexp.MustCompile(skip)
	}
	return s
}

func (s shard) has(name string) bool {
	if s.run != nil && !s.run.MatchString(name) {
		return false
	}
	return s.skip == nil || !s.skip.MatchString(name)
}

func TestEveryNameLandsInExactlyOneShard(t *testing.T) {
	names, err := testNames("../..")
	if err != nil {
		t.Fatal(err)
	}
	w, err := readWeights("weights.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.byName) < 100 {
		t.Fatalf("weights.txt lists %d tests; regenerate it (see the package comment)", len(w.byName))
	}
	if len(names) < 1000 {
		t.Fatalf("found %d test names in the repository; the scanner is missing most of them", len(names))
	}
	// Names the scanner never saw must land somewhere too: a test it misses
	// still has to run.
	probe := append([]string{}, names...)
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // seeded test input
	const alphabet = "09AZ_azé日"
	for range 2000 {
		b := []rune(names[rng.Intn(len(names))])
		b = b[:rng.Intn(len(b)+1)]
		for j := rng.Intn(4); j > 0; j-- {
			b = append(b, []rune(alphabet)[rng.Intn(len([]rune(alphabet)))])
		}
		probe = append(probe, string(b))
	}
	probe = append(probe, "", "Test", "\U0010ffff")

	for _, n := range []int{1, 2, 3, 4} {
		shards := make([]shard, n)
		for i := range shards {
			shards[i] = compile(patterns(names, w.of, i+1, n))
		}
		for _, name := range probe {
			hit := 0
			for _, s := range shards {
				if s.has(name) {
					hit++
				}
			}
			if hit != 1 {
				t.Fatalf("of %d shards, %q is in %d", n, name, hit)
			}
		}
		// Each shard's share of the recorded time is within the largest single
		// test of an even split: a cut can only fall between two tests.
		// (Unlisted names from other packages count at the default here too,
		// as they do when cutting.)
		load := make([]float64, n)
		var total, biggest float64
		for _, name := range names {
			v := w.of(name)
			total += v
			biggest = max(biggest, v)
			for i, s := range shards {
				if s.has(name) {
					load[i] += v
				}
			}
		}
		for i, l := range load {
			if even := total / float64(n); l < even-biggest || l > even+biggest {
				t.Errorf("of %d shards, shard %d carries %.0fs of %.0fs", n, i+1, l, total)
			}
		}
	}
}

func TestCutsNeverLeaveAShardEmpty(t *testing.T) {
	names := []string{"A", "B", "C", "D"}
	heavy := func(name string) float64 {
		if name == "A" {
			return 100
		}
		return 1
	}
	for n := 1; n <= len(names); n++ {
		starts := cuts(names, heavy, n)
		for k := 1; k < n; k++ {
			if starts[k] <= starts[k-1] || starts[k] >= len(names) {
				t.Fatalf("of %d shards, cuts %v leave one empty", n, starts)
			}
		}
	}
	// Fewer names than shards: the first takes them all, the rest run nothing.
	if run, skip := patterns(names[:1], heavy, 1, 3); run != "" || skip != "" {
		t.Errorf("shard 1 of 3 over one name: run %q skip %q", run, skip)
	}
	if run, _ := patterns(names[:1], heavy, 2, 3); run != "^$" {
		t.Errorf("shard 2 of 3 over one name: run %q", run)
	}
}

func TestRecordKeepsTheSlowestPackage(t *testing.T) {
	in := strings.Join([]string{
		`{"Action":"pass","Package":"a","Test":"TestSlow","Elapsed":3}`,
		`{"Action":"pass","Package":"b","Test":"TestSlow","Elapsed":1.5}`,
		`{"Action":"pass","Package":"a","Test":"TestSlow/sub","Elapsed":9}`,
		`{"Action":"fail","Package":"a","Test":"TestFast","Elapsed":0.04}`,
		`{"Action":"pass","Package":"b","Test":"TestOnlyInB","Elapsed":1}`,
		`{"Action":"output","Package":"a","Test":"TestOther","Elapsed":7}`,
		`{"Action":"pass","Package":"a","Elapsed":30}`,
	}, "\n")
	var out strings.Builder
	if err := record(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "w.txt")
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := readWeights(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.of("TestSlow"); got != 3 {
		t.Errorf("TestSlow weighs %v, want package a's 3:\n%s", got, out.String())
	}
	if _, listed := w.byName["TestOnlyInB"]; listed {
		t.Errorf("a test from the faster package was listed:\n%s", out.String())
	}
	if _, listed := w.byName["TestFast"]; listed || w.of("TestFast") != 0.04 {
		t.Errorf("TestFast should fall to the default 0.04:\n%s", out.String())
	}
	if err := record(strings.NewReader(""), &out); err == nil {
		t.Error("an empty stream made a weights file")
	}
}

func TestNoWeightsFileMeansEqualWeights(t *testing.T) {
	w, err := readWeights(filepath.Join(t.TempDir(), "missing.txt"))
	if err != nil || w.of("TestAnything") != 1 {
		t.Fatalf("got %v, %v", w.of("TestAnything"), err)
	}
}

func TestAtOrAfterAgreesWithStringOrder(t *testing.T) {
	words := []string{"", "A", "Test", "TestA", "TestB", "TestAb", "TestA_", "TestZ", "Testa", "Testé", "Test9", "TestX.Y"}
	for _, p := range words {
		re := regexp.MustCompile(atOrAfter(p))
		for _, w := range words {
			if got, want := re.MatchString(w), w >= p; got != want {
				t.Errorf("atOrAfter(%q) on %q = %v, want %v", p, w, got, want)
			}
		}
	}
}

func TestTopLevelSkipsMethodsAndHelpers(t *testing.T) {
	src := "func TestA(t *testing.T) {}\nfunc (s) TestB() {}\nfunc helper() {}\nfunc FuzzC(f *testing.F) {}\nfunc ExampleD() {}\n  func TestIndented() {}\nfunc TestG[T any](t *testing.T) {}\n"
	matches := topLevel.FindAllStringSubmatch(src, -1)
	got := make([]string, 0, len(matches))
	for _, m := range matches {
		got = append(got, m[1])
	}
	want := []string{"TestA", "FuzzC", "ExampleD", "TestG"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
