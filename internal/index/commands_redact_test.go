package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	mk := func(id, project string) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: project,
			Messages: []model.Message{
				{Role: "user", Text: "run it"},
				{Role: "command", Text: "go test ./..."},
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
	if use.ByProject["local"] != 2 || use.ByProject["imported:peer"] != 3 {
		t.Fatalf("per-project counts wrong: %v", use.ByProject)
	}
	if use.Sessions != 5 {
		t.Errorf("machine-wide count wrong: %d", use.Sessions)
	}
}
