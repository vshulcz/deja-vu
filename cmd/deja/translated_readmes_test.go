package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The repository ships a README per language. Two things about that set rot
// silently, and both are invisible to a reader who does not read the language.
//
// The first is the switcher: every one of these files carries the same list of
// twelve links, and it is the only way a reader lands on their own. Add a
// language and forget one file, and that language is reachable from eleven
// pages and not the twelfth; rename a file and every switcher on every page
// points at a 404.
//
// The second is the harness roster — the `Claude Code · Cline · …` block that
// each translation spells out in full. It is a count claim written thirty-four
// times over, and it went stale in Chinese before while the English pages were
// current (#3100). Comparing the roster against the registry catches that in
// any language, because the names are the same everywhere even when nothing
// around them is.
// Three of these live in the root because the outside world already links them
// by that path — the site, the plugin listings, the directory submissions — and
// the nine added later live under docs/readme so the root stays readable. The
// switcher therefore has to be written relative to whichever file carries it,
// which is one more thing to get wrong by hand and the reason this is a test.
var readmeLanguages = []struct {
	file, name string
}{
	{"README.md", "English"},
	{"README.zh.md", "简体中文"},
	{"docs/readme/README.zh-TW.md", "繁體中文"},
	{"README.ja.md", "日本語"},
	{"docs/readme/README.ko.md", "한국어"},
	{"docs/readme/README.es.md", "Español"},
	{"docs/readme/README.pt.md", "Português"},
	{"docs/readme/README.fr.md", "Français"},
	{"docs/readme/README.de.md", "Deutsch"},
	{"docs/readme/README.ru.md", "Русский"},
	{"docs/readme/README.tr.md", "Türkçe"},
	{"docs/readme/README.hi.md", "हिन्दी"},
}

// switcherFor is the line the file at current must contain: every language
// linked from where current sits, its own name plain.
func switcherFor(current string) string {
	var b strings.Builder
	b.WriteString(`<p align="center">`)
	for i, l := range readmeLanguages {
		if i > 0 {
			b.WriteString(" | ")
		}
		if l.file == current {
			b.WriteString(l.name)
			continue
		}
		b.WriteString(`<a href="` + readmeHref(current, l.file) + `">` + l.name + `</a>`)
	}
	b.WriteString("</p>")
	return b.String()
}

// readmeHref is the path from one README to another, as a browser reading the
// first one would resolve it.
func readmeHref(from, to string) string {
	up := strings.Repeat("../", strings.Count(from, "/"))
	if strings.Contains(from, "/") && filepath.Dir(from) == filepath.Dir(to) {
		return filepath.Base(to)
	}
	return up + to
}

func TestEveryTranslatedReadmeExistsAndSwitchesToAllTheOthers(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, l := range readmeLanguages {
		b, err := os.ReadFile(filepath.Join(root, l.file))
		if err != nil {
			t.Errorf("%s is linked from every other README and is not here: %v", l.file, err)
			continue
		}
		if want := switcherFor(l.file); !strings.Contains(string(b), want) {
			t.Errorf("%s does not carry the language switcher every other README carries:\nwant %s", l.file, want)
		}
	}

	// The check above compares the file against a line this test builds, so it
	// only ever proves the file agrees with the formula. This one reads the
	// hrefs back out of the file and resolves each one from where that file
	// sits, which is what a reader's browser does — so a hand-edited link is
	// caught even if the formula that produced it was wrong.
	for _, l := range readmeLanguages {
		b, err := os.ReadFile(filepath.Join(root, l.file))
		if err != nil {
			continue
		}
		line := switcherLine(string(b))
		if line == "" {
			t.Errorf("%s has no line naming every language", l.file)
			continue
		}
		dir := filepath.Dir(filepath.Join(root, l.file))
		links := 0
		for _, m := range hrefPattern.FindAllStringSubmatch(line, -1) {
			if strings.HasPrefix(m[1], "http") {
				continue
			}
			links++
			if _, err := os.Stat(filepath.Join(dir, m[1])); err != nil {
				t.Errorf("the switcher in %s points at %q, which does not resolve from there: %v", l.file, m[1], err)
			}
		}
		if links != len(readmeLanguages)-1 {
			t.Errorf("the switcher in %s carries %d links; there are %d other languages", l.file, links, len(readmeLanguages)-1)
		}
	}
}

var hrefPattern = regexp.MustCompile(`href="([^"]+)"`)

// switcherLine is the one line that names the last language in the list, which
// is the switcher wherever it has been moved to in the file.
func switcherLine(text string) string {
	last := readmeLanguages[len(readmeLanguages)-1].name
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, last) && strings.Contains(line, "href=") {
			return line
		}
	}
	return ""
}

// rosterHead is where the list starts. It is the same three names in every
// language because they are product names, which is what makes this checkable
// at all.
const rosterHead = "Claude Code · Cline · Codex CLI"

// rosterTail closes it: "Zed" and the sentence-ending mark, which is a full
// stop in the Latin-script pages and an ideographic one in the Chinese pages.
var rosterTail = regexp.MustCompile(`Zed\s*[.。]`)

func TestTheTranslatedReadmesListEveryHarnessTheRegistryHas(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	// English and Japanese carry the capability matrix instead of a prose
	// roster, and the two English tests already count that. Every file that
	// does spell the list out has to spell all of it.
	listed := 0
	for _, l := range readmeLanguages {
		b, err := os.ReadFile(filepath.Join(root, l.file))
		if err != nil {
			continue // the test above reports a missing file
		}
		text := string(b)
		start := strings.Index(text, rosterHead)
		if start < 0 {
			continue
		}
		end := rosterTail.FindStringIndex(text[start:])
		if end == nil {
			t.Errorf("%s starts the harness roster and never closes it on Zed", l.file)
			continue
		}
		block := strings.ReplaceAll(text[start:start+end[1]], "\n", " ")
		got := len(strings.Split(block, "·"))
		if got != n {
			t.Errorf("%s lists %d harnesses; the registry has %d", l.file, got, n)
		}
		listed++
	}
	if listed < 8 {
		t.Fatalf("only %d READMEs spell out the roster — the phrasing changed and this test stopped seeing it", listed)
	}
}
