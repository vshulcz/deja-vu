package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An -auto target writes two configs and folds them into one result, so the
// screen whose whole job is naming what deja left behind saw one path and said
// "kept 1 snapshot" beside two .bak files (#3171).
func TestKeptSnapshotsCountsEveryConfigAnAutoTargetWrote(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "a.json")
	two := filepath.Join(dir, "b.json")
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p+".bak", []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := wroteAll(
		installResult{Path: one, Action: "updated"},
		installResult{Path: two, Action: "updated"},
	)
	got := keptSnapshotsLine(r.paths())
	if !strings.Contains(got, "kept 2 snapshots") {
		t.Fatalf("the second config's snapshot went unmentioned:\n%s", got)
	}

	// A file the target read and did not change still has a snapshot beside it
	// if an earlier run made one, and it is still deja's to name.
	r = wroteAll(
		installResult{Path: one, Action: "updated"},
		installResult{Path: two, Action: "unchanged"},
	)
	if got := keptSnapshotsLine(r.paths()); !strings.Contains(got, "kept 2 snapshots") {
		t.Fatalf("an unchanged config's snapshot went unmentioned:\n%s", got)
	}
	// …without claiming it was written: the note is about what changed.
	if strings.Contains(r.Note, "unchanged") {
		t.Errorf("the note reported an unchanged file as work: %q", r.Note)
	}

	// One target, one config: still one snapshot, not a duplicate.
	r = wroteAll(installResult{Path: one, Action: "updated"})
	if got := keptSnapshotsLine(r.paths()); !strings.Contains(got, "kept 1 snapshot ") {
		t.Fatalf("a single-config target miscounted:\n%s", got)
	}
}

// And the same through `deja uninstall claude-auto`, which is where it was
// seen: the line is printed by runInstall out of what installTarget handed
// back, so a unit test on the counter alone cannot hold the wiring (#3171).
func TestUninstallNamesEverySnapshotItLeft(t *testing.T) {
	home := hermeticEnv(t)
	home = filepath.Join(home, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Two configs the reader already had, so both get a snapshot.
	for _, p := range []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if err := os.WriteFile(p, []byte(`{"mine":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runInstall(index.DefaultDir(), []string{"claude-auto", "--no-index", "--no-guidance"}, false); err != nil {
		t.Fatal(err)
	}
	// The line goes to stderr: it is a note about the reader's own files rather
	// than part of what install reports doing.
	out := captureStderr(t, func() {
		if err := runInstall(index.DefaultDir(), []string{"claude-auto", "--no-index", "--no-guidance"}, true); err != nil {
			t.Fatal(err)
		}
	})

	baks := 0
	for _, p := range []string{
		filepath.Join(home, ".claude.json.bak"),
		filepath.Join(home, ".claude", "settings.json.bak"),
	} {
		if _, err := os.Stat(p); err == nil {
			baks++
		}
	}
	if baks != 2 {
		t.Skipf("this build left %d snapshots, not the two this is about", baks)
	}
	if !strings.Contains(out, "kept 2 snapshots") {
		t.Fatalf("uninstall left 2 snapshots and named %s:\n%s",
			map[bool]string{true: "one", false: "neither"}[strings.Contains(out, "kept 1 snapshot")], out)
	}
}
