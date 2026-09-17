package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every harness whose install writes a `/deja` command has a row, and the row
// names the file — including the two whose command is not called `deja`, which
// is the part a reader needs and the part that was wrong for a release (#3655).
func TestEveryCommandFileHasADoctorRow(t *testing.T) {
	hermeticEnv(t)
	rows := map[string]string{}
	for _, c := range doctorCommandFiles() {
		if c.path == "" {
			t.Errorf("%s: row has no path", c.name)
		}
		rows[c.name] = c.path
	}
	for _, name := range installTargetNames() {
		h := guidanceHarness(name)
		want := commandFilePath(h)
		if want == "" {
			continue
		}
		if got, ok := rows[h]; !ok {
			t.Errorf("`deja install %s` writes %s and doctor has no row for it", name, want)
		} else if got != want {
			t.Errorf("%s: row names %s, install writes %s", h, got, want)
		}
	}
	if rows["gemini"] == "" || !strings.HasSuffix(rows["gemini"], "deja-search.toml") {
		t.Errorf("gemini's row must name the file install writes, got %q", rows["gemini"])
	}
}

// Three states, because only one of them is a reason to leave a file alone.
func TestCommandFileStateSeparatesMissingFromSomebodyElses(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "none.md")
	if got := commandFileState(missing); got != "missing" {
		t.Errorf("no file: %q, want missing", got)
	}
	ours := filepath.Join(dir, "deja.md")
	if err := os.WriteFile(ours, []byte(markdownCommand("/bin/deja")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := commandFileState(ours); got != "written" {
		t.Errorf("our own file: %q, want written", got)
	}
	theirs := filepath.Join(dir, "mine.md")
	if err := os.WriteFile(theirs, []byte("# my own command\nrun the thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := commandFileState(theirs); got != "someone else's" {
		t.Errorf("a stranger's file: %q, want someone else's", got)
	}
}

// The section is printed, with its heading, on a machine with nothing wired.
func TestDoctorPrintsTheCommandsSection(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	doctorCommands(&out)
	text := out.String()
	if !strings.HasPrefix(text, "Commands:\n") {
		t.Fatalf("no heading:\n%s", text)
	}
	for _, name := range []string{"claude-code", "gemini", "copilot-chat"} {
		if !strings.Contains(text, "  "+name) {
			t.Errorf("no row for %s:\n%s", name, text)
		}
	}
}
