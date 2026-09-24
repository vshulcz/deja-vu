package main

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every package under extensions/ is installed by the command extensions/README.md
// gives for it, and that command has to reach the two pages people install
// from. The getting-started page was written when there were three packages
// and kept saying "three more" as the directory grew to seven; README.md's
// table stopped at six, and neither said a word (#1579).
//
// The check is on each package's install command rather than on its directory
// name. A bare name is a substring trap — `pi` is inside "pipe" and "api", so a
// page that never mentions pi would pass — while an install command appears
// only where someone actually wrote down how to install it.
func TestEveryExtensionIsListedWhereItIsInstalledFrom(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "extensions"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) < 5 {
		t.Fatalf("found %d packages under extensions/ — the listing is not being read: %v", len(dirs), dirs)
	}

	// The table in extensions/README.md is the source of truth:
	// | [`name/`](name) | Published as | `install command` |
	row := regexp.MustCompile("(?m)^\\|\\s*\\[`([^`/]+)/`\\]\\([^)]*\\)\\s*\\|[^|]*\\|\\s*(.+?)\\s*\\|\\s*$")
	install := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(repoFile(t, "extensions/README.md")), -1) {
		install[m[1]] = strings.Trim(m[2], "`")
	}

	pages := map[string]string{
		"README.md":                       string(repoFile(t, "README.md")),
		"docs/readme/README.zh.md":        string(repoFile(t, "docs/readme/README.zh.md")),
		"docs/guide/getting-started.html": html.UnescapeString(string(repoFile(t, "docs/guide/getting-started.html"))),
	}

	for _, dir := range dirs {
		cmd, ok := install[dir]
		if !ok {
			t.Errorf("extensions/%s/ has no row in extensions/README.md", dir)
			continue
		}
		for name, page := range pages {
			if !strings.Contains(page, cmd) {
				t.Errorf("%s does not say how to install extensions/%s/ — want %q", name, dir, cmd)
			}
		}
	}
}

// The pages stopped counting in prose. A number written beside a list is a
// second copy of the list's length, and it is the copy nobody updates: "three
// more" survived four new packages. The lists say what they hold; the prose
// does not repeat it.
func TestInstallPagesDoNotCountTheirLists(t *testing.T) {
	guide := string(repoFile(t, "docs/guide/getting-started.html"))
	for _, stale := range []string{"Three more keep", "Six harnesses can pull", "wires all three", "all six install"} {
		if strings.Contains(guide, stale) {
			t.Errorf("getting-started still counts a list in prose: %q", stale)
		}
	}
	if strings.Contains(string(repoFile(t, "README.md")), "wires all six of these") {
		t.Error("README still counts its package table in prose")
	}
}
