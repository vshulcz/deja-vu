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
