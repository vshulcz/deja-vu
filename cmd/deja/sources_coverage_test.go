package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// `deja sources` answers "where did deja look", and the empty-state advice
// points people at it. Its harness list was hand-maintained and had fallen four
// behind the registry: cline, roo, pi and openclaw were absent while their
// sessions — 33 of them here — sat in the index.
func TestSourcesListsEveryHarnessInTheRegistry(t *testing.T) {
	hermeticEnv(t)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	printSources(t.TempDir())
	_ = w.Close()
	os.Stdout = old
	out := <-done

	for _, h := range sources.Registry() {
		if !strings.Contains(out, h.Name) {
			t.Errorf("deja sources never mentions %q, so nobody can tell whether deja looked there", h.Name)
		}
	}
}
