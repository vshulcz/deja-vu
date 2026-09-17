package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every harness whose install writes a `/deja` command has a row, and the row
// names the file — including the ones whose command is not called `deja`, which
// is the part a reader needs and the part that was wrong for a release (#3655).
func TestEveryCommandFileHasADoctorRow(t *testing.T) {
	hermeticEnv(t)
	rows := map[string]doctorCommandFile{}
	for _, c := range doctorCommandFiles() {
		if c.path == "" {
			t.Errorf("%s: row has no path", c.name)
		}
		rows[c.name] = c
	}
	for _, name := range installTargetNames() {
		h := guidanceHarness(name)
		want := commandFilePath(h)
		if want == "" {
			continue
		}
		if got, ok := rows[h]; !ok {
			t.Errorf("`deja install %s` writes %s and doctor has no row for it", name, want)
		} else if got.path != want {
			t.Errorf("%s: row names %s, install writes %s", h, got.path, want)
		}
	}
	// And the harnesses with no file of deja's still have a row, saying the
	// skill is the command. An omitted row read as "deja has no command here"
	// when the truth is that it is installed under another name (#3667).
	for _, name := range skillIsTheCommandHarnesses() {
		got, ok := rows[name]
		if !ok {
			t.Errorf("%s has no row; its skill is its command", name)
			continue
		}
		if !got.skill {
			t.Errorf("%s: row is not marked as a skill command", name)
		}
		if got.path != commandSkillPath(name) {
			t.Errorf("%s: row names %s, the skill is at %s", name, got.path, commandSkillPath(name))
		}
		if !strings.Contains(got.path, "skills") {
			t.Errorf("%s: row names %s, which is not a skill file", name, got.path)
		}
		if commandFilePath(name) != "" {
			t.Errorf("%s: deja writes a command file at %s as well as a skill", name, commandFilePath(name))
		}
	}
}

// Four states, and only one of them is a reason to leave a file alone.
func TestCommandFileStateSeparatesMissingFromSomebodyElses(t *testing.T) {
	dir := t.TempDir()
	if got := (doctorCommandFile{path: filepath.Join(dir, "none.md")}).state(); got != "missing" {
		t.Errorf("no file: %q, want missing", got)
	}
	ours := filepath.Join(dir, "deja.md")
	if err := os.WriteFile(ours, []byte(markdownCommand("/bin/deja")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (doctorCommandFile{path: ours}).state(); got != "written" {
		t.Errorf("our own file: %q, want written", got)
	}
	if got := (doctorCommandFile{path: ours, skill: true}).state(); got != "skill" {
		t.Errorf("a skill row: %q, want skill", got)
	}
	theirs := filepath.Join(dir, "mine.md")
	if err := os.WriteFile(theirs, []byte("# my own command\nrun the thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (doctorCommandFile{path: theirs}).state(); got != "someone else's" {
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
	for _, name := range []string{"claude-code", "cursor", "copilot-chat", "gemini", "codex"} {
		if !strings.Contains(text, "  "+name) {
			t.Errorf("no row for %s:\n%s", name, text)
		}
	}
	// And what `skill` means, or the word is a state nobody can read.
	if !strings.Contains(text, "the skill is the command there") {
		t.Errorf("the skill state is never explained:\n%s", text)
	}
}
