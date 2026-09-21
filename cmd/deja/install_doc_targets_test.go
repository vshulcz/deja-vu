package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docInstallTarget finds every `deja install <target>` the site and the READMEs
// print. A guide page is where someone gets the command from, so a name there
// that the binary refuses is a broken instruction: memory-for-dsh.html said
// `deja install dsh-auto` and the only names that resolved were `deepseek` and
// `deepseek-auto`.
var docInstallTarget = regexp.MustCompile(`deja install ([a-z][a-z0-9-]*)`)

func TestEveryInstallTargetTheDocsPrintResolves(t *testing.T) {
	root := filepath.Join("..", "..")
	named := map[string][]string{}
	for _, dir := range []string{"docs", "."} {
		err := filepath.Walk(filepath.Join(root, dir), func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() {
				switch fi.Name() {
				case ".git", "node_modules", "vendor", "cmd", "internal", "extensions", "fixtures":
					return filepath.SkipDir
				}
				return nil
			}
			switch filepath.Ext(p) {
			case ".md", ".html", ".txt":
			default:
				return nil
			}
			// The changelog records what the command was called at the time.
			if strings.Contains(filepath.Base(p), "CHANGELOG") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			rel := strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(p, root)), "/")
			for _, m := range docInstallTarget.FindAllStringSubmatch(string(b), -1) {
				named[m[1]] = append(named[m[1]], rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(named) < 10 {
		t.Fatalf("found only %d install targets in the docs — the scan is broken", len(named))
	}

	names := make([]string, 0, len(named))
	for n := range named {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
			// Only the refusal matters. A target may fail for a reason of its
			// own here — an unreadable config, a harness this machine has no
			// directory for — and that is not what this test is about.
			if _, err := installTarget(name, "/usr/local/bin/deja", false); err != nil &&
				strings.Contains(err.Error(), "unknown target") {
				t.Errorf("%s prints `deja install %s`, and the binary refuses it: %v",
					strings.Join(dedupe(named[name]), ", "), name, err)
			}
		})
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
