package main

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

// `search` was dispatched and undocumented, the same omission #749 found in the
// completions and from the same cause: it comes from the switch in run(), not
// from the commands map, so lists built from that map miss it (#753).
func TestHelpNamesEveryDispatchedCommand(t *testing.T) {
	// Plumbing invoked by harnesses, and the wrappers whose bare form is a
	// search rather than a command.
	internal := map[string]bool{
		"warmup-status": true, "help": true, "aider": true, "goose": true,
		"--help": true, "-h": true, "--version": true, "-version": true,
	}
	var names []string
	for name := range commands {
		if strings.HasPrefix(name, "hook-") || internal[name] {
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
