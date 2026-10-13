package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// whatChangedTerminal returns the terminal message for the first run of a new
// version: a short line plus the top three changes since the last version this
// machine ran. It returns "" when there is nothing new to say. Whichever
// surface (terminal or hook) asks first records the version, so the message
// shows once per version (#4619).
func whatChangedTerminal(dir string) string {
	items, current := whatChangedFor(dir)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "deja v%s is new — what changed:\n", current)
	for _, it := range items {
		fmt.Fprintf(&b, "  - %s\n", it)
	}
	return b.String()
}

// whatChangedNote is the same news as one line for the session-start note, so
// the agent can mention it: the first change and how many more there are.
func whatChangedNote(dir string) string {
	items, current := whatChangedFor(dir)
	if len(items) == 0 {
		return ""
	}
	head := fmt.Sprintf("deja v%s is new — ", current)
	tail := ""
	if len(items) > 1 {
		tail = fmt.Sprintf(" (+%d more)", len(items)-1)
	}
	// One line: the change is cut to what is left of noteWidth, since a
	// changelog sentence runs to 140 characters on its own.
	item := items[0]
	if room := noteWidth - termwidth.Columns(head) - termwidth.Columns(tail); termwidth.Columns(item) > room {
		item = cutChangelogItem(item, room)
	}
	return head + item + tail
}

// cutChangelogItem cuts a changelog sentence to width columns, ellipsis
// included, without ending inside a `code span` or a (#NNNN) reference: half
// a command or half an issue number points nowhere.
func cutChangelogItem(item string, width int) string {
	cut := termwidth.Cut(item, width-1)
	if strings.Count(cut, "`")%2 == 1 {
		cut = cut[:strings.LastIndex(cut, "`")]
	}
	if i := strings.LastIndex(cut, "(#"); i >= 0 && !strings.Contains(cut[i:], ")") {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:(—") + "…"
}

// whatChangedFor returns the top three changes since the last version this
// machine ran, and the current version. The last version lives in its own
// file beside the index, `<index dir>.lastversion`, not in the release stamp,
// because the detached release look rewrites that stamp and could drop it.
// Every new version is recorded, including the first one this feature ever
// sees: that run has no earlier version to count from, so it stays quiet, and
// the next upgrade is the first to say anything. A record that cannot be
// written keeps it quiet too, so it never repeats on every run.
func whatChangedFor(dir string) ([]string, string) {
	if !filepath.IsAbs(dir) || version == "" || version == "dev" {
		return nil, ""
	}
	marker := dir + ".lastversion"
	last := ""
	if b, err := os.ReadFile(marker); err == nil {
		last = strings.TrimSpace(string(b))
	}
	if last == version {
		return nil, ""
	}
	if err := os.WriteFile(marker, []byte(version+"\n"), 0o600); err != nil || last == "" {
		return nil, ""
	}
	items := whatChangedItems(dejavu.Changelog, last, version, 3)
	if len(items) == 0 {
		return nil, ""
	}
	return items, version
}

// whatChangedItems returns up to n bullet items from every CHANGELOG section
// newer than lastVersion, in changelog order (newest first). It returns an
// empty slice when the current version is not newer than lastVersion, or when
// no newer section has any items.
func whatChangedItems(changelog, lastVersion, currentVersion string, n int) []string {
	if currentVersion == "" || currentVersion == "dev" {
		return nil
	}
	if lastVersion == currentVersion {
		return nil
	}
	if order, ok := compareUpdateVersions(lastVersion, currentVersion); !ok || order >= 0 {
		// No last version, or the current version is not newer: nothing to say.
		return nil
	}
	var items []string
	for _, section := range changelogSections(changelog) {
		ver := section.version
		if order, ok := compareUpdateVersions(ver, lastVersion); !ok || order <= 0 {
			// This section is not newer than the last version this machine ran.
			continue
		}
		for _, line := range strings.Split(section.body, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") {
				items = append(items, firstSentence(strings.TrimPrefix(line, "- ")))
				if len(items) >= n {
					return items
				}
			}
		}
	}
	return items
}

// changelogSection is one version's section of the CHANGELOG.
type changelogSection struct {
	version string
	body    string
}

// changelogSections parses the CHANGELOG into its version sections, in the
// order they appear (newest first).
func changelogSections(changelog string) []changelogSection {
	var sections []changelogSection
	lines := strings.Split(changelog, "\n")
	var cur *changelogSection
	for _, line := range lines {
		if strings.HasPrefix(line, "## [") {
			if cur != nil {
				sections = append(sections, *cur)
			}
			ver := strings.TrimPrefix(line, "## [")
			if i := strings.IndexByte(ver, ']'); i >= 0 {
				ver = ver[:i]
			}
			cur = &changelogSection{version: normalizeUpdateVersion(ver)}
			continue
		}
		if cur != nil {
			cur.body += line + "\n"
		}
	}
	if cur != nil {
		sections = append(sections, *cur)
	}
	return sections
}

// firstSentence shortens a changelog bullet, which is often a whole paragraph,
// to its first sentence, and caps that at 140 characters, so three of them
// still read as a short list in a terminal.
func firstSentence(item string) string {
	if i := strings.Index(item, ". "); i >= 0 {
		item = item[:i+1]
	}
	if r := []rune(item); len(r) > 140 {
		item = strings.TrimSpace(string(r[:139])) + "…"
	}
	return item
}
