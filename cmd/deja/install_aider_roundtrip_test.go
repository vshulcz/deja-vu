package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// install then uninstall gives the reader's ~/.aider.conf.yml back byte for
// byte, whatever form their read: was in. A CRLF file was not matched as a
// block list, so install wrote a second read: key and aider, which keeps the
// last, dropped the reader's files — and uninstall left a bare `read:` behind
// (#4330). A scalar or flow read: came back as a block list, and a file that
// held only `read: []` was deleted (#4331).
func TestAiderConfigRoundTrips(t *testing.T) {
	for _, c := range []struct{ name, conf string }{
		{"crlf block", "model: x\r\nread:\r\n  - C.md\r\n"},
		{"scalar", "# my settings\nmodel: gpt-4o\nread: CONVENTIONS.md\ndark-mode: true\n"},
		{"flow", "model: gpt-4o\nread: [CONVENTIONS.md, \"a b.md\"]\n"},
		{"empty flow", "read: []\n"},
		{"crlf scalar", "model: x\r\nread: C.md\r\n"},
		{"block, key comment", "read:  # mine\n  - C.md\nmodel: x\n"},
		{"block, comment under key", "read:\n  # the house rules\n  - C.md\n"},
		{"block at column 0", "read:\n- C.md\n- D.md\nmodel: x\n"},
		{"lf block", "model: x\nread:\n  - C.md\n"},
		{"null", "model: x\nread: ~\n"},
		{"quoted flow", "read: [\"#notes.md\", 'k: v.md']\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
			path := filepath.Join(home, ".aider.conf.yml")
			if err := os.WriteFile(path, []byte(c.conf), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installAider("/bin/echo", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			got := aiderConf(t, home)
			lf := strings.ReplaceAll(got, "\r\n", "\n")
			if n := strings.Count("\n"+lf, "\nread:"); n != 1 {
				t.Fatalf("read: written %d times, aider reads only the last:\n%q", n, got)
			}
			if !confReadsDejaContext(lf) {
				t.Fatalf("the context file is not under read:\n%q", got)
			}
			for _, f := range []string{"C.md", "CONVENTIONS.md", "a b.md", "D.md"} {
				if strings.Contains(c.conf, f) && !strings.Contains(got, f) {
					t.Fatalf("install dropped %s:\n%q", f, got)
				}
			}
			if strings.Contains(c.conf, "\r\n") && strings.Count(got, "\n") != strings.Count(got, "\r\n") {
				t.Errorf("a CRLF config came back with mixed endings:\n%q", got)
			}
			// A second install is a no-op.
			res, err := installAider("/bin/echo", false)
			if err != nil {
				t.Fatalf("second install: %v", err)
			}
			if again := aiderConf(t, home); again != got || res.Action != "unchanged" {
				t.Errorf("a second install changed the file (%s):\n%q\nthen\n%q", res.Action, got, again)
			}
			if _, err := installAider("/bin/echo", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("uninstall removed a config the reader wrote: %v", err)
			}
			if string(b) != c.conf {
				t.Errorf("not the file it was:\n%q\nwant\n%q", b, c.conf)
			}
		})
	}
}

// confReadsDejaContext reports whether a top-level read: list names deja's
// context file.
func confReadsDejaContext(s string) bool {
	inRead := false
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if l[0] != ' ' && !strings.HasPrefix(t, "- ") {
			inRead = strings.HasPrefix(l, "read:")
			continue
		}
		if inRead && strings.Contains(t, "aider-context.md") {
			return true
		}
	}
	return false
}

// Two read: keys are the reader's to settle: aider keeps the last, and joining
// either one hides a file somewhere.
func TestAiderRefusesTwoReadKeys(t *testing.T) {
	for _, conf := range []string{
		"read: a.md\nread:\n  - b.md\n",
		"read:\n  - a.md\nmodel: x\nread:\n  - b.md\n",
	} {
		if _, err := addAiderReadEntry(conf, "/x/aider-context.md"); err == nil {
			t.Errorf("%q was joined instead of refused", conf)
		}
	}
}

// A flow item keeps its quotes when install makes the list a block list:
// unquoted, "#notes.md" reads as a comment and 'k: v.md' as a mapping, and
// aider loses both files.
func TestAiderFlowListKeepsQuotes(t *testing.T) {
	if got, _ := addAiderReadEntry("read: ~\n", "/x/aider-context.md"); strings.Contains(got, "- ~") {
		t.Errorf("a null read: became a null item:\n%s", got)
	}
	got, err := addAiderReadEntry("read: [\"#notes.md\", 'k: v.md', plain.md]\n", "/x/aider-context.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  - \"#notes.md\"\n", "  - 'k: v.md'\n", "  - plain.md\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
}

// A block scalar or an alias under read: is not a file name to promote: the
// indicator became a list item and the text under it was left dangling below
// deja's entry.
func TestAiderRefusesBlockScalarRead(t *testing.T) {
	for _, conf := range []string{"read: |\n  x.md\n", "read: >-\n  x.md\n", "read: *files\n"} {
		if got, err := addAiderReadEntry(conf, "/x/aider-context.md"); err == nil {
			t.Errorf("%q was rewritten instead of refused:\n%s", conf, got)
		}
	}
}

// The record of the line install promoted is per config, and the newest one is
// what uninstall puts back. A second promotion left the first record beside it,
// and the record is stored sorted, so a later process could read the old line
// and leave the reader's new one as a block list.
func TestAiderReadWasKeepsOnlyTheLatestLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".aider.conf.yml")
	newProcess := func() {
		recordWiring([]string{"aider"}, false)
		blocksAddedThisRun = nil
		blocksForgottenThisRun = map[string]bool{}
	}
	t.Cleanup(newProcess)
	for _, conf := range []string{"read: b.md\n", "read: a.md\n"} {
		if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installAider("/bin/echo", false); err != nil {
			t.Fatal(err)
		}
		newProcess()
	}
	if _, err := installAider("/bin/echo", true); err != nil {
		t.Fatal(err)
	}
	if got := aiderConf(t, home); got != "read: a.md\n" {
		t.Errorf("uninstall gave back %q, want the line the last install promoted", got)
	}
}
