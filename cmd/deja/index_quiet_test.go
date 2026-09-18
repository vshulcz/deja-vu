package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `deja index --quiet` for the runs nobody is watching (#1827).
//
// The flag suppresses the success reporting and nothing else: a scheduled run
// that goes quiet about failure is worse than one that prints a line, so the
// tests below pin both halves.

func indexEnv(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	dir := filepath.Join(t.TempDir(), "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	return dir
}

func TestIndexQuietSaysNothingOnSuccess(t *testing.T) {
	indexEnv(t)
	// Build once so the second run is the ordinary "already fresh" path a
	// shell profile or hook actually hits.
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("first index: %v", err)
	}
	out, err := captureRunStderr(t, "index", "--quiet")
	if err != nil {
		t.Fatalf("index --quiet: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("index --quiet printed %q", out)
	}
}

func TestIndexWithoutQuietStillReports(t *testing.T) {
	indexEnv(t)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("first index: %v", err)
	}
	out, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(out, "up to date") {
		t.Fatalf("index without --quiet said %q, expected the up-to-date line", out)
	}
}

// The issue asks which of the two the flag covers. Both: they print through
// the same sink, and a reader would not expect one to stay noisy.
func TestIndexQuietCoversRebuild(t *testing.T) {
	indexEnv(t)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("first index: %v", err)
	}
	out, err := captureRunStderr(t, "index", "--quiet", "--rebuild")
	if err != nil {
		t.Fatalf("index --quiet --rebuild: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("index --quiet --rebuild printed %q", out)
	}
}

// Quiet is about the success line. A run that cannot build still has to say
// why, or a scheduled job stops working and nothing says so.
func TestIndexQuietStillReportsFailure(t *testing.T) {
	indexEnv(t)
	// A regular file where a parent directory should be, rather than an
	// unwritable directory: a mode bit denies nothing on Windows or to root,
	// so the build succeeded there and the test read that as quiet swallowing
	// the error. Creating a directory underneath a file fails everywhere.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(blocker, "index.db"))

	out, err := captureRunStderr(t, "index", "--quiet")
	if err == nil {
		t.Fatal("index --quiet into an unwritable directory reported success")
	}
	// The error carries the explanation; whichever stream it lands on, the
	// command must not have swallowed it.
	if strings.TrimSpace(out) == "" && strings.TrimSpace(err.Error()) == "" {
		t.Fatal("index --quiet failed silently")
	}
}

func TestIndexStillRefusesUnknownFlags(t *testing.T) {
	indexEnv(t)
	if _, err := captureRunStderr(t, "index", "--quiett"); err == nil {
		t.Fatal("unknown flag accepted")
	}
}
