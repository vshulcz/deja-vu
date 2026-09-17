package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3681 gave six writers one predicate for "this entry is deja's own,
// whatever the build was called", and each one was tested where it was
// changed. ZCode's recogniser kept the narrow test and nobody noticed until
// Windows, where the test binary is `deja.test.exe`: its uninstall read its own
// entries as a stranger's and left the whole block.
//
// So the question is asked of every auto target at once, and in the shape the
// machine in #3681 was in — entries naming a build that is not the one
// installing now. The writers seed themselves: install, rewrite every path
// they wrote to a name of a build nobody has, install again, and require the
// count of deja hook lines not to grow.
func TestASecondBuildAdoptsEveryTargetsHookEntries(t *testing.T) {
	tmp := hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	autoTargets := []string{}
	for _, name := range installTargetNames() {
		if strings.HasSuffix(name, "-auto") {
			autoTargets = append(autoTargets, name)
		}
	}
	installAll := func() {
		for _, target := range autoTargets {
			if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
				continue // refused here, so it wrote nothing to count
			}
		}
	}
	installAll()
	before := dejaHookLineCounts(t, home)
	if len(before) == 0 {
		t.Fatal("no hook lines were written, so there is nothing to collapse")
	}

	// The state the machine in #3681 was in: every entry names a build that is
	// not the one about to install.
	stranger := filepath.Join(tmp, "scratch", "deja-cont")
	// The launcher where there is one, and the binary's own path where there is
	// not: on Windows deja writes no launcher (a .cmd cannot be exec'd), so
	// going by the launcher alone skipped this test on the one platform whose
	// leg found the bug it exists for.
	planted := dejaLauncherPath()
	if planted == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		planted = exe
	}
	replaced := 0
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		text := string(b)
		for _, was := range []string{planted, jsonEscapedPath(planted)} {
			if was == "" || !strings.Contains(text, was) {
				continue
			}
			text = strings.ReplaceAll(text, was, strings.ReplaceAll(stranger, `\`, `\\`))
			replaced++
		}
		if text == string(b) {
			return nil
		}
		return os.WriteFile(p, []byte(text), fi.Mode().Perm())
	})
	if replaced == 0 {
		t.Fatal("no file named the binary the install wrote, so there is no stranger to plant")
	}

	installAll()
	after := dejaHookLineCounts(t, home)
	for path, n := range after {
		if was, ok := before[path]; ok && n > was {
			t.Errorf("%s: hook lines grew from %d to %d — the second build did not recognise its own entries",
				strings.TrimPrefix(path, home), was, n)
		}
	}
	for path := range after {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "deja-cont") {
			t.Errorf("%s still runs the build that is not there:\n%s",
				strings.TrimPrefix(path, home), b)
		}
	}
}

// dejaHookLineCounts is how many command lines running one of deja's hooks
// each file under home holds.
func dejaHookLineCounts(t *testing.T, home string) map[string]int {
	t.Helper()
	out := map[string]int{}
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		n := 0
		for _, cmd := range hookCommandLinesIn(string(b)) {
			for _, field := range strings.Fields(cmd) {
				if hookNames[strings.Trim(field, `"'`)] {
					n++
					break
				}
			}
		}
		if n > 0 {
			out[p] = n
		}
		return nil
	})
	return out
}

// jsonEscapedPath is the path as a JSON string holds it, for the generated
// plugins that carry it through %q.
func jsonEscapedPath(p string) string {
	return strings.ReplaceAll(p, `\`, `\\`)
}
