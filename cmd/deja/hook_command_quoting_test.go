package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every hook line deja writes is a command a shell parses, and the launcher
// lives under the home directory — so a home with a space in its name made all
// of them unrunnable: `sh: /tmp/deja: No such file or directory`, on every
// prompt, for every harness (#3692).
//
// The invariant, rather than a case per writer: install every auto target into
// a home whose name has a space in it, then take each command line out of the
// files and require its first word to name a file that is there.
func TestEveryHookLineRunsFromAHomeWithASpace(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "a home with spaces")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	spaceyEnv(t, home)
	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			continue // a target that refuses here has written nothing
		}
	}
	lines := 0
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, cmd := range hookCommandLinesIn(string(b)) {
			lines++
			first := firstShellWord(cmd)
			if _, serr := os.Stat(first); serr != nil {
				t.Errorf("%s runs a command whose first word is not a file:\n  %s\n  first word: %s",
					strings.TrimPrefix(p, home), cmd, first)
			}
		}
		return nil
	})
	if lines == 0 {
		t.Fatal("no hook command lines found — the walk is not reading what the writers wrote")
	}
	t.Logf("checked %d hook command lines", lines)
}

// spaceyEnv is hermeticEnv with a home of the test's choosing, so the paths
// the writers build carry the space.
func spaceyEnv(t *testing.T, home string) {
	t.Helper()
	hermeticEnv(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(home, ".local", "share", "goose"))
}

// hookCommandLinesIn pulls the command lines out of a config: any quoted
// string that runs one of deja's subcommands. Every format here quotes its
// values — JSON, JSONC, TOML, YAML — so one reader answers for all of them.
func hookCommandLinesIn(text string) []string {
	var out []string
	for _, quote := range []byte{'"', '\''} {
		for _, span := range quotedSpans(text, quote) {
			cmd := quotedPathUnescape.Replace(span)
			if !hasDejaSubcommand(cmd) {
				continue
			}
			out = append(out, cmd)
		}
	}
	return out
}

// quotedSpans are the contents of every quoted string in the text, taking an
// escaped quote as part of the string rather than its end.
func quotedSpans(text string, quote byte) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != quote {
			continue
		}
		for j := i + 1; j < len(text); j++ {
			if text[j] == '\n' {
				i = j
				break
			}
			if text[j] != quote {
				continue
			}
			if j > 0 && text[j-1] == '\\' {
				continue
			}
			out = append(out, text[i+1:j])
			i = j
			break
		}
	}
	return out
}

// hasDejaSubcommand reports whether a command line runs one of deja's own
// subcommands, which is what makes it a line deja wrote rather than a path
// mentioned in prose.
func hasDejaSubcommand(cmd string) bool {
	for _, field := range strings.Fields(cmd) {
		if dejaSubcommandInConfigs[strings.Trim(field, `"'`)] {
			return strings.Contains(cmd, "deja")
		}
	}
	return false
}

// firstShellWord is the command a shell would run: the quoted span when the
// line starts with a quote, and the first whitespace-delimited word otherwise.
func firstShellWord(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if q := cmd[0]; q == '\'' || q == '"' {
		if end := strings.IndexByte(cmd[1:], q); end >= 0 {
			return cmd[1 : 1+end]
		}
	}
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		return cmd[:i]
	}
	return cmd
}

// A path that needs no quoting is left alone: a config full of quoted paths
// where none of them needs it is a diff in somebody's dotfiles for nothing.
func TestHookLinesAreNotQuotedWhenTheyNeedNotBe(t *testing.T) {
	if got := hookRun("/usr/local/bin/deja", "hook-prompt"); got != "/usr/local/bin/deja hook-prompt" {
		t.Errorf("an ordinary path was quoted: %q", got)
	}
	got := hookRun("/Users/A B/bin/deja", "hook-prompt", "--strict")
	if !strings.HasSuffix(got, " hook-prompt --strict") || !strings.ContainsAny(got[:1], `"'`) {
		t.Errorf("a path with a space was not quoted: %q", got)
	}
}

// And an entry already written in the unquoted form is deja's own to rewrite,
// not somebody else's line wrapping deja's hook — which is what left a broken
// machine broken through every later install.
func TestTheUnquotedFormIsRecognisedAsOurs(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "a home with spaces", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "deja-hook")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := exe + " hook-prompt"
	if kind := hookCommandKindOf(stale, hookRun(exe, "hook-prompt")); kind != hookDejas {
		t.Errorf("the unquoted form read as %v, not deja's own", kind)
	}
	// A wrapper somebody wrote around deja's hook is still theirs, space in
	// the path or not.
	wrapper := "env FOO=1 " + exe + " hook-prompt"
	if kind := hookCommandKindOf(wrapper, hookRun(exe, "hook-prompt")); kind != hookWrapsDejas {
		t.Errorf("a wrapper read as %v, not as a wrapper", kind)
	}
}
