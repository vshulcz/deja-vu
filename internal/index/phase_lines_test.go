package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Off a terminal, a build says each phase on a line of its own, with the
// count it works through (#5396).
func TestABuildWritesOneLinePerPhase(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p1", "a.jsonl"), claudeLine("a1", "2026-01-02T03:04:05Z", "phaseneedle"))
	var out bytes.Buffer
	SetPhaseLines(&out)
	err := Ensure(dir, "", true, nil)
	SetPhaseLines(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"deja: finding transcripts", "deja: reading sessions — 1 file", "deja: indexing messages — 1 message", "deja: writing index"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q line:\n%s", want, out.String())
		}
	}
	n := out.Len()
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() != n {
		t.Errorf("a build after SetPhaseLines(nil) still wrote:\n%s", out.String()[n:])
	}
}

// The lines a pass skipped are counted for the run that skipped them, and a
// pass that reads nothing new adds none (#5396).
func TestSkippedThisRunCountsOnlyWhatThisPassRead(t *testing.T) {
	root, dir := allHarnessEnv(t)
	p := filepath.Join(root, "claude", "-tmp-p1", "a.jsonl")
	write(t, p, claudeLine("a1", "2026-01-02T03:04:05Z", "skipneedle"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	before := SkippedThisRun()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := SkippedThisRun() - before; got != 1 {
		t.Fatalf("the pass that read the bad line counted %d, want 1", got)
	}
	before = SkippedThisRun()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := SkippedThisRun() - before; got != 0 {
		t.Fatalf("a pass that read nothing new counted %d", got)
	}
}

// Past the first second each line says how far into the build it is.
func TestAPhaseLineSaysHowFarIn(t *testing.T) {
	var out bytes.Buffer
	p := &phaseWriter{w: &out, started: time.Now().Add(-3 * time.Second)}
	p.phase("writing index", 12)
	if got := out.String(); got != "deja: writing index (3s in)\n" {
		t.Fatalf("got %q", got)
	}
}
