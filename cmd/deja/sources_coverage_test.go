package main

import (
	"bytes"
	"os"
	"path/filepath"
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

	// The security model tells a reader that an exclude line has to name one of
	// the stores this command prints, and it spells that count in words. It said
	// twenty-five while the command printed thirty-five — ten stores a reader
	// would not know they could exclude. The number is the row count, not the
	// registry's: deja prints its own store too.
	rows := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			rows++
		}
	}
	word, ok := countWords[rows]
	if !ok {
		t.Fatalf("deja sources prints %d rows and this test has no word for it; add one", rows)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "SECURITY-MODEL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "the " + word + " `deja sources` prints"; !strings.Contains(string(b), want) {
		t.Errorf("docs/SECURITY-MODEL.md does not say %q — deja sources prints %d rows", want, rows)
	}
}
