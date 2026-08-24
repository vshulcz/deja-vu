package main

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"testing"
)

// `search` was dispatched and undocumented, the same omission #749 found in the
// completions and from the same cause: it comes from the switch in run(), not
// from the commands map, so lists built from that map miss it (#753).
func TestHelpNamesEveryDispatchedCommand(t *testing.T) {
	// The wrappers whose bare form is a search rather than a command, and the
	// flag spellings.
	internal := map[string]bool{
		"aider": true, "goose": true,
		"--help": true, "-h": true, "--version": true, "-version": true,
	}
	var names []string
	for name := range commands {
		// helpHidden rather than a blanket `hook-` skip: the skip let help
		// document five hook commands and hide five, including hook-context,
		// with nothing to notice (#1654). A hook is now either documented or
		// named in helpHidden, where the reason lives.
		if helpHidden[name] || internal[name] {
			continue
		}
		names = append(names, name)
	}
	names = append(names, "search", "show", "last")

	help := captureHelp(t)
	line := regexp.MustCompile(`(?m)^\s+deja ([a-z][a-z0-9-]*)`)
	documented := map[string]bool{}
	for _, m := range line.FindAllStringSubmatch(help, -1) {
		documented[m[1]] = true
	}
	for _, name := range names {
		if !documented[name] {
			t.Errorf("deja help does not name %q", name)
		}
	}
	// A hidden name must still be a real command, or helpHidden is excusing a
	// ghost — the way a stale entry would quietly re-open the gap it was added
	// to close (#1654).
	for name := range helpHidden {
		if name == "help" {
			continue // answered before dispatch, so it is not in the map
		}
		if _, ok := commands[name]; !ok {
			t.Errorf("helpHidden excuses %q, which is not a command", name)
		}
	}
	// And the reverse: help must not teach a command that no longer exists.
	for name := range documented {
		if _, ok := commands[name]; ok {
			continue
		}
		switch name {
		case "search", "show", "last", "aider", "goose":
		default:
			t.Errorf("deja help names %q, which is not dispatched", name)
		}
	}
}

func captureHelp(t *testing.T) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	printUsage()
	_ = w.Close()
	os.Stdout = old
	return <-done
}
