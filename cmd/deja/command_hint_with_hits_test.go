package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureBoth runs one command and returns its stdout and stderr separately:
// this test is about which of the two the hint lands on.
func captureBoth(t *testing.T, args ...string) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	outCh, errCh := make(chan string, 1), make(chan string, 1)
	go func() { b, _ := io.ReadAll(rOut); outCh <- string(b) }()
	go func() { b, _ := io.ReadAll(rErr); errCh <- string(b) }()
	_ = run(args)
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	return <-outCh, <-errCh
}

// A wrong guess at a command falls through to search, and the hint that says so
// used to run only when the search came back empty (#674). A typo whose word is
// in the history never got there: the reader saw a conversation they did not
// ask for and nothing about the command they meant (#2197).
func TestAMistypedCommandIsNamedEvenWhenTheSearchMatches(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claude, "alpha", "one.jsonl"), "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"` + at +
			`","message":{"role":"user","content":"we ran deja stats and doctro on the box"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// The premise: this word is in the history, so the search does find it and
	// the hint is not standing in for an empty result.
	out, said := captureBoth(t, "doctro")
	if !strings.Contains(out, "doctro on the box") {
		t.Fatalf("the fallback search found nothing, so this measures nothing:\n%s\n%s", out, said)
	}
	if !strings.Contains(said, "did you mean `deja doctor`?") {
		t.Errorf("a mistyped command that matched something says nothing about the command:\n%s", said)
	}
	// On stderr, so a script reading the results is not fed the hint.
	if strings.Contains(out, "did you mean") {
		t.Errorf("the hint is on stdout, where the results are:\n%s", out)
	}

	// A bare search of more than one word is a search, however close its first
	// word sits to a command name.
	if _, saidPhrase := captureBoth(t, "doctro", "box"); strings.Contains(saidPhrase, "did you mean") {
		t.Errorf("a multi-word bare search was read as a guess at a command:\n%s", saidPhrase)
	}
	// The explicit form is someone searching for the word, and stays quiet.
	_, saidExplicit := captureBoth(t, "search", "doctro")
	if strings.Contains(saidExplicit, "did you mean") {
		t.Errorf("`deja search doctro` is a search, not a guess at a command:\n%s", saidExplicit)
	}
}
