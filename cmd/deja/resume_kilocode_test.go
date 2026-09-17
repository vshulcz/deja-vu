package main

import (
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A Kilo session from the CLI database carries the directory it ran in, never
// the database file — so the rule that told the two halves apart by comparing
// the path with the database refused every CLI session, which is the only half
// `kilo -s` is for (#3677).
func TestKiloResumeTellsTheCLIFromTheExtension(t *testing.T) {
	hermeticEnv(t)
	cli := model.Session{Harness: "kilocode", ID: "ses_abc123", Path: "/work/api"}
	dir, cmd, err := resumeCommand(cli)
	if err != nil {
		t.Fatalf("a CLI session was refused: %v", err)
	}
	if cmd != "kilo -s ses_abc123" {
		t.Errorf("command = %q", cmd)
	}
	if dir != "/work/api" {
		t.Errorf("dir = %q, want the session's own directory", dir)
	}

	// An extension task is the half with no terminal command.
	roots := sources.KiloRoots()
	if len(roots) == 0 {
		t.Skip("no Kilo globalStorage root on this platform to build a task path from")
	}
	task := model.Session{
		Harness: "kilocode",
		ID:      "task-1",
		Path:    filepath.Join(roots[0], "tasks", "task-1", "api_conversation_history.json"),
	}
	if _, _, err := resumeCommand(task); err == nil {
		t.Error("an editor task was given a CLI command")
	}
}
