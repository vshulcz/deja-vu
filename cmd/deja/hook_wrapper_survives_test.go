package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A program of the reader's own that takes deja's subcommand is theirs, even
// when its name starts like deja's: `deja-wrapper.cmd hook-prompt` was read as
// a deja build, so an install rewrote it to deja's line and an uninstall
// deleted it (#4728 review). Builds under other names are still deja's
// (#3681).
func TestHookTokenIsDejasTellsABuildFromAScript(t *testing.T) {
	forgetWrittenExes()
	t.Cleanup(forgetWrittenExes)
	for tok, want := range map[string]bool{
		"/usr/local/bin/deja":                 true,
		`C:\deja\deja.exe`:                    true,
		"/home/me/.config/deja/bin/deja-hook": true,
		"/scratch/deja-cont":                  true,
		`C:/build/deja-prog2.exe`:             true,
		"/tmp/go-build1/b001/deja.test":       true,
		`C:\Temp\deja.test.exe`:               true,
		`"C:/tools/deja-wrapper.cmd"`:         false,
		"/opt/bin/deja-run.sh":                false,
		"/usr/bin/dejavu-notify":              false,
		"/usr/bin/dejagnu":                    false,
	} {
		if got := hookTokenIsDejas(tok); got != want {
			t.Errorf("hookTokenIsDejas(%s) = %v, want %v", tok, got, want)
		}
	}
}

// The same, end to end through every -auto target: after an install, each
// hook line deja wrote is turned into the reader's own wrapper of the same
// shape, then deja installs and uninstalls again. Every wrapper line has to be
// there, unchanged, after both. Run once from a plain home and once from a
// home with a space, where the lines are quoted.
func TestAWrapperNamedLikeDejaSurvivesInstallAndUninstall(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, homeName := range []string{"home", "a home with spaces"} {
		for _, wrapper := range []string{"deja-wrapper.cmd", "deja-run.sh"} {
			for _, target := range installTargetNames() {
				if !strings.HasSuffix(target, "-auto") {
					continue
				}
				t.Run(homeName+"/"+wrapper+"/"+target, func(t *testing.T) {
					home := filepath.Join(t.TempDir(), homeName)
					if err := os.MkdirAll(home, 0o755); err != nil {
						t.Fatal(err)
					}
					spaceyEnv(t, home)
					forgetWrittenExes()
					t.Cleanup(forgetWrittenExes)
					if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
						t.Skipf("install refused here: %v", err)
					}
					// What the lines name: the launcher, or on Windows the
					// binary, written with forward slashes.
					named := filepath.ToSlash(hookCommandExe(self))
					theirs := filepath.ToSlash(filepath.Join(filepath.Dir(named), wrapper))
					seeded := map[string]int{}
					_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
						if err != nil || !fi.Mode().IsRegular() {
							return nil //nolint:nilerr // unreadable entries are skipped
						}
						b, rerr := os.ReadFile(p) //nolint:gosec // the walk is over the test's own temp home
						if rerr != nil {
							return nil //nolint:nilerr // unreadable files are skipped
						}
						text := string(b)
						// A file deja generates whole, or a block it marks as its
						// own, holds no line of the reader's to keep.
						if dejaOwnsFile(home, p) || strings.Contains(text, "managed by `deja install") {
							return nil
						}
						n := 0
						for _, line := range hookCommandLinesIn(text) {
							if firstShellWord(line) == named {
								n++
							}
						}
						if n == 0 {
							return nil
						}
						text = strings.ReplaceAll(text, named+" hook-", theirs+" hook-")
						text = strings.ReplaceAll(text, named+`\" hook-`, theirs+`\" hook-`)
						text = strings.ReplaceAll(text, named+`' hook-`, theirs+`' hook-`)
						if c := strings.Count(text, theirs); c > 0 {
							if err := os.WriteFile(p, []byte(text), fi.Mode()); err != nil { //nolint:gosec // the walk is over the test's own temp home
								t.Fatal(err)
							}
							seeded[p] = c
						}
						return nil
					})
					if len(seeded) == 0 {
						t.Skip("no shell hook line to turn into a wrapper")
					}
					for _, step := range [][]string{{"install", target, "--no-index"}, {"uninstall", target}} {
						_, _ = captureRun(t, step...)
						for p, want := range seeded {
							b, _ := os.ReadFile(p)
							if got := strings.Count(string(b), theirs); got != want {
								t.Errorf("after %s, %s names %s %d times, want %d:\n%s",
									step[0], strings.TrimPrefix(p, home), wrapper, got, want, b)
							}
						}
					}
				})
			}
		}
	}
}

// dejaOwnsFile reports whether deja generated the file whole: it sits in a
// directory named deja, or is named deja itself.
func dejaOwnsFile(home, p string) bool {
	rel, err := filepath.Rel(home, p)
	if err != nil {
		return false
	}
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if part == "deja" || strings.HasPrefix(part, "deja.") {
			return true
		}
	}
	return false
}
