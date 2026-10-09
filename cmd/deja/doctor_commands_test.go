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
	// No row says `skill`, so there is nothing to explain.
	if strings.Contains(text, "the skill is the command there") {
		t.Errorf("the note explains a state no row shows:\n%s", text)
	}
	// Once one does, what it means is said, or the word is a state nobody
	// can read.
	skill := commandSkillPath("codex")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: deja-history\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorCommands(&out)
	if !strings.Contains(out.String(), "the skill is the command there") {
		t.Errorf("the skill state is never explained:\n%s", out.String())
	}
}

// Continue's and goose's commands are items in a config.yaml rather than files
// of their own, and doctor had no row for either: a removed or renamed /deja
// looked the same as a working one (#4374). The state comes from the item.
func TestDoctorReadsConfigYAMLCommands(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_CONTINUE_ROOT", "")
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	t.Setenv("GOOSE_PATH_ROOT", "")
	row := func(name string) doctorCommandFile {
		t.Helper()
		for _, c := range doctorCommandFiles() {
			if c.name == name {
				return c
			}
		}
		t.Fatalf("no %s row", name)
		return doctorCommandFile{}
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, path string
		install    func() error
		other      string
	}{
		{"continue", continueConfigPath(),
			func() error { _, err := installContinue("/bin/deja", false); return err },
			"prompts:\n  - name: deja\n    description: my own\n    prompt: grep my notes\n"},
		{"goose", filepath.Join(gooseConfigDir(), "config.yaml"),
			func() error { _, err := installGooseCommand("/bin/deja", false); return err },
			"slash_commands:\n  - command: \"deja\"\n    recipe_path: /home/me/notes-recipe.yaml\n"},
	}
	for _, c := range cases {
		// A config with no item of that name: missing, though the file is there.
		write(c.path, "models: []\n")
		if got := row(c.name).state(); got != "missing" {
			t.Errorf("%s with no item: %q, want missing", c.name, got)
		}
		if row(c.name).path != c.path {
			t.Errorf("%s: row names %s, install writes %s", c.name, row(c.name).path, c.path)
		}
		write(c.path, c.other)
		if got := row(c.name).state(); got != "someone else's" {
			t.Errorf("%s with the reader's own /deja: %q, want someone else's", c.name, got)
		}
		write(c.path, "models: []\n")
		if err := c.install(); err != nil {
			t.Fatal(err)
		}
		if got := row(c.name).state(); got != "written" {
			t.Errorf("%s after install: %q, want written", c.name, got)
		}
	}
}
