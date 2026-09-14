package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/bench"
)

// The gate this benchmark exists to be: one long escape-heavy value must not
// cost more than the rest of the store put together by orders of magnitude.
// That is what `sqlite3 -json` did for two months while every other benchmark
// stayed flat — 412s on a value this size against 0.04s, because no benchmark
// read a store at all (#3553, #3552).
func TestBenchReadHoldsTheLongValueToTheRestOfTheStore(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	// On a deadline, because the failure this guards against does not make the
	// benchmark slower by a little: a reader that is quadratic in what it
	// escapes takes about seven minutes on this store, which would read as a
	// package timeout rather than as a failed gate.
	type result struct {
		report readReport
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, err := measureRead(bench.Seed)
		done <- result{r, err}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("reading a store of half a megabyte and one long value took over a minute")
	}
	report, err := got.report, got.err
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped != "" {
		t.Skipf("benchmark skipped itself: %s", report.Skipped)
	}
	if len(report.Classes) != 2 {
		t.Fatalf("measured %d stores, want the ordinary one and the one with a long value: %#v", len(report.Classes), report.Classes)
	}
	plain, long := report.Classes[0], report.Classes[1]
	if plain.Sessions == 0 || plain.Messages == 0 {
		t.Fatalf("the ordinary store read nothing: %#v", plain)
	}
	// Every message of the corpus has to come back, or the benchmark is timing
	// a store the reader mostly skips. The reader gates on `"type":"text"` in
	// the head of a part, so a fixture that writes its fields in another order
	// halves what it measures without saying so.
	corpus := bench.Generate(bench.Seed)
	want := 0
	for _, s := range corpus.Sessions {
		want += len(s.Messages)
	}
	if plain.Messages != want {
		t.Fatalf("read %d messages of the corpus's %d — the reader skipped what the fixture wrote", plain.Messages, want)
	}
	// The long value is one more message, and it is the only difference
	// between the two stores — otherwise the ratio below compares two
	// different corpora.
	if long.Sessions != plain.Sessions || long.Messages != plain.Messages+2 {
		t.Errorf("stores differ by more than the long value: %#v vs %#v", plain, long)
	}
	if long.StoreMB < 4 {
		t.Errorf("the long value did not land: the store is %.2f MB", long.StoreMB)
	}
	// Generous on purpose: this is a shared runner, and what it has to catch is
	// a reader that is quadratic in what it escapes, which lands four orders of
	// magnitude away rather than near any threshold.
	if report.Ratio > 100 {
		t.Errorf("one %d KB value cost %.0fx the rest of the store (%.1fms against %.1fms) — the read is not linear in what it escapes",
			readBenchLongValueKB, report.Ratio, long.WallMS, plain.WallMS)
	}
}

func TestBenchReadReportNamesBothStores(t *testing.T) {
	var b strings.Builder
	printReadReport(&b, readReport{
		CorpusHash: "abc",
		Seed:       1,
		Classes: []readClass{
			{Name: "ordinary store", WallMS: 18.9, StoreMB: 0.28, Sessions: 500, Messages: 1000},
			{Name: "one long tool output", WallMS: 53.6, StoreMB: 4.55, Sessions: 500, Messages: 1002},
		},
		Ratio: 2.8,
	})
	out := b.String()
	for _, want := range []string{"ordinary store", "one long tool output", "2.8x"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not say %q:\n%s", want, out)
		}
	}
}

func TestBenchReadSaysWhenItCannotRun(t *testing.T) {
	var b strings.Builder
	printReadReport(&b, readReport{Skipped: "sqlite3 is not installed, and the database-backed stores are read through it"})
	if !strings.Contains(b.String(), "sqlite3 is not installed") {
		t.Fatalf("a skipped run has to say why:\n%s", b.String())
	}
}
