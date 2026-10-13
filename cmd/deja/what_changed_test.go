package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// The first run of a new version says once what it brings: a short line plus
// the top three changes from the CHANGELOG since the last version this machine
// ran (#4619).
func TestWhatChangedItemsSaysWhatIsNew(t *testing.T) {
	changelog := `# Changelog

## [0.2.0] - 2026-10-09

### Added

- first new thing
- second new thing
- third new thing
- fourth new thing

### Fixed

- a fix

## [0.1.0] - 2026-10-01

### Added

- old thing
`
	items := whatChangedItems(changelog, "0.1.0", "0.2.0", 3)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %v", len(items), items)
	}
	for i, want := range []string{"first new thing", "second new thing", "third new thing"} {
		if items[i] != want {
			t.Errorf("item %d = %q, want %q", i, items[i], want)
		}
	}
}

// A two-version jump takes the top three items across every section newer than
// the last version this machine ran, not only the current version's section.
func TestWhatChangedItemsAcrossTwoVersions(t *testing.T) {
	changelog := `# Changelog

## [0.3.0] - 2026-10-09

### Added

- newest thing

## [0.2.0] - 2026-10-04

### Added

- middle thing one
- middle thing two

## [0.1.0] - 2026-10-01

### Added

- old thing
`
	// Last ran 0.1.0, now on 0.3.0: the top three come from 0.3.0 and 0.2.0.
	items := whatChangedItems(changelog, "0.1.0", "0.3.0", 3)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %v", len(items), items)
	}
	for i, want := range []string{"newest thing", "middle thing one", "middle thing two"} {
		if items[i] != want {
			t.Errorf("item %d = %q, want %q", i, items[i], want)
		}
	}
}

// The message shows once per version: the same version again says nothing.
func TestWhatChangedShowsOncePerVersion(t *testing.T) {
	changelog := `## [0.2.0] - 2026-10-09

### Added

- a thing
`
	if got := whatChangedItems(changelog, "0.2.0", "0.2.0", 3); len(got) != 0 {
		t.Errorf("same version said %v, want nothing", got)
	}
	if got := whatChangedItems(changelog, "0.2.0", "0.1.0", 3); len(got) != 0 {
		t.Errorf("an older version said %v, want nothing", got)
	}
	if got := whatChangedItems(changelog, "", "0.2.0", 3); len(got) != 0 {
		t.Errorf("a first run ever said %v, want nothing", got)
	}
	if got := whatChangedItems(changelog, "0.1.0", "dev", 3); len(got) != 0 {
		t.Errorf("a dev build said %v, want nothing", got)
	}
}

// whatChangedTerminal and whatChangedNote share the once-per-version record:
// whichever runs first marks the version shown, so the other says nothing.
func TestWhatChangedTerminalAndNoteShareRecord(t *testing.T) {
	withVersion(t, "0.2.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	// The machine last ran 0.1.0.
	if err := os.WriteFile(dir+".lastversion", []byte("0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The terminal runs first and says what is new.
	term := whatChangedTerminal(dir)
	if !strings.Contains(term, "deja v0.2.0 is new") {
		t.Fatalf("terminal said %q, want the new-version line", term)
	}
	if b, _ := os.ReadFile(dir + ".lastversion"); strings.TrimSpace(string(b)) != "0.2.0" {
		t.Errorf("record kept %q, want 0.2.0", b)
	}

	// The hook then says nothing: the version was already shown.
	if got := whatChangedNote(dir); got != "" {
		t.Errorf("the hook said %q after the terminal, want nothing", got)
	}
}

// whatChangedNote is the one-line hook note, and it stays quiet when the
// terminal already showed the version.
func TestWhatChangedNoteOneLine(t *testing.T) {
	withVersion(t, "0.2.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".lastversion", []byte("0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	note := whatChangedNote(dir)
	if !strings.Contains(note, "deja v0.2.0 is new") {
		t.Fatalf("note said %q, want the new-version line", note)
	}
	if strings.Contains(note, "\n") {
		t.Errorf("note is not one line: %q", note)
	}
	// The embedded changelog's bullets run past 140 characters; the note
	// cuts the first to what fits.
	if w := termwidth.Columns(note); w > noteWidth {
		t.Errorf("note is %d columns, over %d: %q", w, noteWidth, note)
	}
	// The terminal then says nothing.
	if got := whatChangedTerminal(dir); got != "" {
		t.Errorf("the terminal said %q after the hook, want nothing", got)
	}
}

// whatChangedFor stays quiet when the index dir is relative, so the stamp
// would land in whatever directory deja was run from.
func TestWhatChangedForRelativeDir(t *testing.T) {
	withVersion(t, "0.2.0")
	if items, _ := whatChangedFor(filepath.Join(".cache", "deja", "index")); len(items) != 0 {
		t.Errorf("a relative index dir said %v, want nothing", items)
	}
}

// The first version this feature sees has nothing to count from, so it stays
// quiet but records itself; the next upgrade is the first to say what changed.
// Without that record the news would never show for anyone upgrading.
func TestWhatChangedFirstRunRecordsQuietly(t *testing.T) {
	withVersion(t, "0.1.0")
	dir := releaseNoticeEnv(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if items, _ := whatChangedFor(dir); len(items) != 0 {
		t.Fatalf("a first run said %v, want nothing", items)
	}
	if b, _ := os.ReadFile(dir + ".lastversion"); strings.TrimSpace(string(b)) != "0.1.0" {
		t.Fatalf("a first run recorded %q, want 0.1.0", b)
	}
	withVersion(t, "9.9.9")
	if items, current := whatChangedFor(dir); current != "9.9.9" {
		t.Fatalf("the upgrade after a first run said %v (%q), want the 9.9.9 news", items, current)
	}
}

// A changelog bullet is often a paragraph; the list shows its first sentence.
func TestWhatChangedItemsAreShort(t *testing.T) {
	long := "Recall got faster. It now skips sessions it already read, which on one machine " +
		"cut a cold search from four seconds to under one, and the index stays the same size."
	if got := firstSentence(long); got != "Recall got faster." {
		t.Errorf("firstSentence = %q, want the first sentence", got)
	}
	run := strings.Repeat("word ", 60)
	if got := firstSentence(run); len([]rune(got)) > 140 {
		t.Errorf("firstSentence left %d characters, want at most 140", len([]rune(got)))
	}
}

// A cut never ends inside a code span or an issue reference.
func TestWhatChangedCutKeepsCodeAndReferencesWhole(t *testing.T) {
	item := "Session start shows one note at a time, and `deja hook-context --notes` prints the rest later (#4993) for hosts that render it"
	for width := 20; width < len(item); width++ {
		got := cutChangelogItem(item, width)
		if termwidth.Columns(got) > width {
			t.Fatalf("width %d: %q is %d columns", width, got, termwidth.Columns(got))
		}
		if strings.Count(got, "`")%2 == 1 {
			t.Fatalf("width %d: cut inside the code span: %q", width, got)
		}
		if i := strings.LastIndex(got, "(#"); i >= 0 && !strings.Contains(got[i:], ")") {
			t.Fatalf("width %d: cut inside the reference: %q", width, got)
		}
	}
}
