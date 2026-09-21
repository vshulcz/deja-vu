package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The commands page is checked in one direction — every real command has a row
// (#3356) — and nothing checked the other: a document can tell a reader to run
// something the binary would refuse. `deja install dsh-auto` was exactly that
// until #3869, printed on a guide page and rejected by the dispatcher.
//
// The first word after `deja` is a command position only when it is not a
// query: an unknown word is a search by design, so `deja connection pool` is a
// legitimate example and no test can tell it from a typo. A flag after it
// settles that — `deja stats --card` is an invocation, not a query — so this
// takes every documented invocation carrying a flag and requires the
// dispatcher to know its command.
func TestEveryFlaggedInvocationInTheDocsNamesARealCommand(t *testing.T) {
	root := filepath.Join("..", "..")
	files := shippedDocFiles(t, root)
	if len(files) < 80 {
		t.Fatalf("found %d documents to read — the walk is wrong", len(files))
	}

	found := map[string]bool{}
	var offenders []string
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		for _, name := range flaggedCommandNames(string(b)) {
			found[name] = true
			if dispatchKnows(name) {
				continue
			}
			offenders = append(offenders, name+" in "+filepath.ToSlash(rel))
		}
	}
	// The floor is the part that keeps this from passing because the walk
	// stopped finding anything: 21 commands are documented with a flag today.
	if len(found) < 15 {
		t.Fatalf("only %d commands are documented with a flag (%v) — the pattern is wrong", len(found), sortedNames(found))
	}
	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("a document says to run `deja %s`, which the dispatcher does not know", o)
	}
}

// shippedDocFiles is every document a reader or an agent acts on: the site, the
// guides, the READMEs, and the skill and command files deja installs into a
// harness. The changelog is not one of them — it quotes output and mistyped
// commands on purpose, which is the opposite of an instruction.
func shippedDocFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{"docs", "claude-plugin", "codex-plugin", "npm", "."} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", ".claude":
					return filepath.SkipDir
				}
				// The repository root is read for its own files only; the
				// directories that matter have their own entry above.
				if dir == "." && p != base {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Base(p) == "CHANGELOG.md" {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".md", ".html", ".txt":
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// A code span, a `<code>` element, a shell prompt or the start of a line in a
// block, followed within three words by a flag.
var flaggedDeja = regexp.MustCompile("(?m)(?:<code>|`|\\$ |^[ \t]*)deja ([a-z][a-z0-9-]*)((?: +[^\\s<`]+){0,3})")

func flaggedCommandNames(body string) []string {
	var out []string
	for _, m := range flaggedDeja.FindAllStringSubmatch(body, -1) {
		if !strings.Contains(m[2], "--") {
			continue
		}
		out = append(out, m[1])
	}
	return out
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
