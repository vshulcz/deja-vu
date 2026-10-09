package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Every command had a usage line and eleven had nothing else: no description
// in `deja help`, and a --help that repeated the usage line (#4626).
func TestEveryListedCommandHasADescriptionAndAnExample(t *testing.T) {
	for _, e := range helpEntries {
		if strings.TrimSpace(e.desc) == "" {
			t.Errorf("%s has no description", e.name)
		}
		if len(e.examples) == 0 {
			t.Errorf("%s has no example", e.name)
		}
		for _, x := range e.examples {
			if !strings.Contains(x, "deja") {
				t.Errorf("%s example %q does not run deja", e.name, x)
			}
		}
		if len(e.usage) == 0 || !strings.HasPrefix(e.usage[0], "deja "+e.name) {
			t.Errorf("%s usage does not start with its name: %v", e.name, e.usage)
		}
		h := helpForCommand(e.name)
		for _, want := range []string{"Usage:", "deja " + e.name, "Examples:", e.examples[0]} {
			if !strings.Contains(h, want) {
				t.Errorf("%s --help lacks %q:\n%s", e.name, want, h)
			}
		}
	}
}

// One description column, and no row of the page breaks at 80 columns even
// with nothing to fit it to: piped help is what a README or an agent quotes.
func TestTheHelpPageFitsEightyColumnsAsWritten(t *testing.T) {
	col := -1
	for _, line := range strings.Split(usageText(), "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("a line is %d wide: %q", n, line)
		}
		if !strings.HasPrefix(line, "  deja ") || strings.Contains(line, `"`) {
			continue
		}
		rest := line[len("  deja "):]
		gap := strings.Index(rest, "  ")
		if gap < 0 {
			continue
		}
		at := len("  deja ") + gap + len(rest[gap:]) - len(strings.TrimLeft(rest[gap:], " "))
		if col < 0 {
			col = at
		} else if at != col {
			t.Errorf("description at column %d, the page uses %d: %q", at, col, line)
		}
	}
}

// The hook and agent-facing commands come last, after every command a person
// types.
func TestAgentFacingCommandsAreListedLast(t *testing.T) {
	page := usageText()
	agents := strings.Index(page, groupAgents+":")
	if agents < 0 {
		t.Fatal("no section for agent-facing commands")
	}
	for _, e := range helpEntries {
		if e.group == "" {
			continue
		}
		at := strings.Index(page, "  deja "+e.name+" ")
		if at < 0 {
			t.Errorf("%s is not listed", e.name)
			continue
		}
		hook := e.group == groupAgents
		if hook != (at > agents) {
			t.Errorf("%s is in the wrong section", e.name)
		}
	}
}

// `deja help <cmd>` answers what `deja <cmd> --help` does; a word that is not
// a command is an error that says where the list is.
func TestHelpTakesACommand(t *testing.T) {
	hermeticEnv(t)
	a, err := captureRun(t, "help", "blame")
	if err != nil {
		t.Fatal(err)
	}
	b, err := captureRun(t, "blame", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !strings.Contains(a, "deja blame <path>") {
		t.Fatalf("help blame and blame --help differ:\n%s\n---\n%s", a, b)
	}
	if _, err := captureRun(t, "help", "stauts"); err == nil || !strings.Contains(err.Error(), "`deja help` lists them") {
		t.Fatalf("an unknown topic: %v", err)
	}
}

// Piped, a bare deja printed the 106-line page. It points at it instead.
func TestPipedBareDejaPointsAtHelp(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "\n"); n > 6 {
		t.Errorf("bare deja into a pipe printed %d lines:\n%s", n, out)
	}
	for _, want := range []string{"deja <query>", "deja help", "deja brief"} {
		if !strings.Contains(out, want) {
			t.Errorf("the pointer lacks %q:\n%s", want, out)
		}
	}
}

// A name that is no target at all is the whole answer: nothing was done, so
// "finished what it could" describes a run that never started.
func TestInstallOfAnUnknownTargetSaysOnlyThat(t *testing.T) {
	hermeticEnv(t)
	err := runInstall(t.TempDir(), []string{"claud"}, false)
	if err == nil {
		t.Fatal("an unknown target was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, `unknown target "claud" — did you mean "claude-code"?`) {
		t.Errorf("no near miss: %q", msg)
	}
	for _, not := range []string{"finished what it could", "fix what it reports"} {
		if strings.Contains(msg, not) {
			t.Errorf("the refusal says %q about a run that did nothing: %q", not, msg)
		}
	}
}

// An id that names nothing says where ids are listed.
func TestAMissingIDSaysWhereIDsAre(t *testing.T) {
	withTempStores(t)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"show", "share", "resume"} {
		_, err := captureRun(t, cmd, "zzzz9999")
		if err == nil || !strings.Contains(err.Error(), "`deja last` lists recent ones") {
			t.Errorf("%s with a bad id: %v", cmd, err)
		}
	}
}

// The hosts read the hook and status line bytes as they are. Off a terminal
// nothing is added.
func TestTheLineEndIsOnlyForATerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	if err := endLineOnTerminal(f, func(w io.Writer) error {
		_, err := io.WriteString(w, "<deja-recall>x</deja-recall>")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	b, _ := os.ReadFile(f.Name())
	if string(b) != "<deja-recall>x</deja-recall>" {
		t.Fatalf("a file got %q", b)
	}
	var lw lineEndWriter
	lw.w = &bytes.Buffer{}
	_, _ = lw.Write([]byte("a\n"))
	if lw.last != '\n' || lw.n != 2 {
		t.Fatalf("lineEndWriter tracked %q/%d", lw.last, lw.n)
	}
}

// The paste-only footer names the agents found on this machine, not all of
// them.
func TestTheHandoffFooterNamesOnlyAgentsFoundHere(t *testing.T) {
	bin := t.TempDir()
	name := "codex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	got := pasteOnlyFooter("")
	if !strings.Contains(got, "deja handoff --to codex --exec") {
		t.Errorf("codex is on PATH and not offered:\n%s", got)
	}
	if strings.Contains(got, "claude") || strings.Contains(got, "|") {
		t.Errorf("the footer names agents that are not here:\n%s", got)
	}
	t.Setenv("PATH", t.TempDir())
	if got := pasteOnlyFooter(""); !strings.Contains(got, "deja handoff --help") {
		t.Errorf("with no agent here: %q", got)
	}
}
