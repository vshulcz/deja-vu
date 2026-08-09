package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A recurring command can carry a live secret in an env assignment or a URL.
// commands.gob is exported through CommandHistory to a PreToolUse hook, so it
// must be redacted like the record log — the writeSessionsWithSync build path
// used to mine it from raw ss.
func TestCommandsAreRedactedLikeTheRecordLog(t *testing.T) {
	dir := t.TempDir()
	secret := "AKIAIOSFODNN7EXAMPLE"
	cmd := "AWS_ACCESS_KEY_ID=" + secret + " aws s3 ls"
	sess := func(id string) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: "p",
			Messages: []model.Message{
				{Role: "user", Text: "listing the bucket"},
				{Role: "command", Text: cmd},
			},
		}
	}
	// Two sessions so the command clears commandMinSessions.
	ss := []model.Session{sess("a"), sess("b")}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	got := ReadCommands(dir)
	if len(got) == 0 {
		t.Fatal("the recurring command was not recorded")
	}
	for _, u := range got {
		if strings.Contains(u.Command, secret) {
			t.Fatalf("commands.gob stored a live secret: %q", u.Command)
		}
	}
}

// A compound command must not be endorsed on the strength of its first line:
// "git status\nrm -rf /" shares a first line with a stored "git status" but is
// a different, dangerous command.
func TestCommandHistoryRejectsAMultilineLookup(t *testing.T) {
	dir := t.TempDir()
	sess := func(id string) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: "p",
			Messages: []model.Message{
				{Role: "user", Text: "check status"},
				{Role: "command", Text: "git status"},
			},
		}
	}
	ss := []model.Session{sess("a"), sess("b")}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := CommandHistory(dir, "git status"); !ok {
		t.Fatal("the single-line command it did run is not recognised")
	}
	if _, ok := CommandHistory(dir, "git status\nrm -rf /"); ok {
		t.Error("a compound command was endorsed on its harmless first line")
	}
}

// A command that ran only in a withheld project must not surface, and the count
// shown must exclude withheld sessions. ByProject carries the per-project split
// so the caller can apply its trust policy.
func TestCommandsCarryPerProjectCounts(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	mk := func(id, project string) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: project,
			Messages: []model.Message{
				{Role: "user", Text: "run it"},
				{Role: "command", Text: "go test ./...", Time: when},
			},
		}
	}
	ss := []model.Session{
		mk("l1", "local"), mk("l2", "local"),
		mk("p1", "imported:peer"), mk("p2", "imported:peer"), mk("p3", "imported:peer"),
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	use, ok := CommandHistory(dir, "go test ./...")
	if !ok {
		t.Fatal("the recurring command was not recorded")
	}
	if use.ByProject["local"].Sessions != 2 || use.ByProject["imported:peer"].Sessions != 3 {
		t.Fatalf("per-project counts wrong: %v", use.ByProject)
	}
	if use.ByProject["imported:peer"].Last.IsZero() {
		t.Error("per-project last-run not recorded")
	}
	if use.Sessions != 5 {
		t.Errorf("machine-wide count wrong: %d", use.Sessions)
	}
}

// The last-run date must come from allowed projects only. A command run more
// recently in a withheld project must not print that project's date when it
// surfaces from an allowed one.
func TestCommandLastIsPerProject(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	cmd := func(id, project string, when time.Time) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: project,
			Messages: []model.Message{
				{Role: "user", Text: "run"},
				{Role: "command", Text: "make release", Time: when},
			},
		}
	}
	ss := []model.Session{
		cmd("l1", "local", old), cmd("l2", "local", old),
		cmd("p1", "imported:peer", newer), cmd("p2", "imported:peer", newer),
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	use, ok := CommandHistory(dir, "make release")
	if !ok {
		t.Fatal("not recorded")
	}
	// The allowed project's last is the older date; the machine-wide Last is
	// the newer withheld one. The per-project split must keep them apart.
	if !use.ByProject["local"].Last.Equal(old) {
		t.Errorf("allowed project's last-run wrong: %v", use.ByProject["local"].Last)
	}
	if !use.Last.Equal(newer) {
		t.Errorf("machine-wide last should be the newer date: %v", use.Last)
	}
}

// A path synced from Windows uses backslashes; CrossBase must split them like a
// Unix path so a same-file lookup still matches.
func TestCrossBaseSplitsBothSeparators(t *testing.T) {
	cases := map[string]string{
		`C:\src\pkg\main.go`: "main.go",
		"/home/u/proj/x.go":  "x.go",
		`a\b\c`:              "c",
		"bare.txt":           "bare.txt",
	}
	for in, want := range cases {
		if got := CrossBase(in); got != want {
			t.Errorf("CrossBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// Switching IsCurrentVersion/Damaged to the cached manifest read must not let a
// stale cache mask a store that changed under it: the cache is keyed on
// manifest.gob's mtime+size, so a corrupted manifest is still seen as damaged.
func TestCachedManifestChecksSeeCorruption(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "s", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "hi"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	if !IsCurrentVersion(dir) {
		t.Fatal("a fresh build is not current")
	}
	if Damaged(dir) {
		t.Fatal("a fresh build reads as damaged")
	}
	// Corrupt the manifest — a different size/mtime invalidates the cache.
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("not a gob at all, longer than before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Damaged(dir) {
		t.Error("a corrupted manifest is not detected through the cache")
	}
}
