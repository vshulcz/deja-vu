package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// deja used to write guidance into the file gemini reads and now writes the
// shared skill, so the old block is retired on the way past — unless its
// markers are unbalanced, when it is skipped. Skipping is right: the block
// cannot be bounded, and cutting to the next end marker is what deleted a file
// in #1705. Skipping in silence is not, on the one command whose job is to
// leave nothing behind (#2218).
func TestUninstallSaysWhenARetiredBlockHasToStay(t *testing.T) {
	hermeticEnv(t)
	retired := filepath.Join(sources.GeminiHome(), "GEMINI.md")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatal(err)
	}
	whole := "my own gemini notes\n\n" + guidanceStart + "\nold guidance\n" + guidanceEnd + "\n"
	if err := os.WriteFile(retired, []byte(whole), 0o644); err != nil {
		t.Fatal(err)
	}
	// The premise: a block deja can bound is taken out without a word.
	res, err := installGuidance("gemini", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "" {
		t.Fatalf("a block deja removed was announced: %q", res.Note)
	}
	b, err := os.ReadFile(retired)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), guidanceStart) {
		t.Fatalf("the well-formed block was not removed, so this measures nothing:\n%s", b)
	}

	// The same file with its end marker gone.
	half := "my own gemini notes\n\n" + guidanceStart + "\nold guidance\n"
	if err := os.WriteFile(retired, []byte(half), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = installGuidance("gemini", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Note, retired) {
		t.Errorf("nothing names the file deja had to leave alone: %q", res.Note)
	}
	after, err := os.ReadFile(retired)
	if err != nil {
		t.Fatal(err)
	}
	// Left as it was, user's text and all: an unbounded block is not something
	// to guess the end of.
	if string(after) != half {
		t.Errorf("the file deja could not bound was rewritten:\n%s", after)
	}
}
