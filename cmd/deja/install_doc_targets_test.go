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
//
// Checked against the name list rather than by installing: `installTarget`
// writes files, and on Windows several targets write under APPDATA, which
// t.Setenv("HOME") does not move — a run of all of them left state that two
// other tests then read as their own.
var docInstallTarget = regexp.MustCompile(`deja install ([a-z][a-z0-9-]*)`)

func TestEveryInstallTargetTheDocsPrintResolves(t *testing.T) {
	root := filepath.Join("..", "..")
	named := map[string][]string{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			switch fi.Name() {
			case ".git", ".github", "node_modules", "vendor", "cmd", "internal", "fixtures", "tools", "scripts":
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
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, root), string(filepath.Separator)))
		for _, m := range docInstallTarget.FindAllStringSubmatch(string(b), -1) {
			named[m[1]] = append(named[m[1]], rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(named) < 10 {
		t.Fatalf("found only %d install targets in the docs — the scan is broken", len(named))
	}

	known := map[string]bool{}
	for _, n := range installTargetNames() {
		known[n] = true
	}
	for alias := range installTargetAliases {
		known[alias] = true
	}

	names := make([]string, 0, len(named))
	for n := range named {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		if !known[name] {
			t.Errorf("%s prints `deja install %s`, and there is no such target",
				strings.Join(dedupe(named[name]), ", "), name)
		}
	}
}

// Every alias has to name a real target, or it resolves to a switch label that
// is not there and the refusal is worse than the one it was added to fix.
func TestEveryInstallAliasNamesARealTarget(t *testing.T) {
	real := map[string]bool{}
	for _, n := range installTargetNames() {
		real[n] = true
	}
	for alias, canonical := range installTargetAliases {
		if !real[canonical] {
			t.Errorf("alias %q points at %q, which is not a target", alias, canonical)
		}
		if real[alias] {
			t.Errorf("alias %q is also a listed target — one of the two is wrong", alias)
		}
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
