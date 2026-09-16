package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// goose resolves its own directories through etcetera's choose_app_strategy
// with author "Block" — the Apple strategy on macOS — so its sessions live
// under `~/Library/Application Support/Block/goose`, which goose's own comment
// names. deja read `~/.local/share/goose` on every platform, so a mac user who
// had used goose was told `goose missing` (#3642).
//
// The older locations are still read, because an install that predates the
// change has its sessions there: this machine is one of them, which is why the
// single-root reader looked correct from inside it.
func TestGooseDataDirsCoverThePlatformAndTheOlderOnes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_GOOSE_ROOT", "")
	t.Setenv("DEJA_GOOSE_DB", "")

	dirs := GooseDataDirs()
	if len(dirs) < 2 {
		t.Fatalf("candidates = %v, want the platform's and the older ones", dirs)
	}
	joined := strings.Join(dirs, "\n")
	xdg := filepath.Join(home, ".local", "share", "goose")
	if !strings.Contains(joined, xdg) {
		t.Errorf("the XDG location is not a candidate: %v", dirs)
	}
	// The older layout with the author segment, which tokscale reads as
	// "legacy Block/goose" — seen in the wild, so it stays a candidate.
	if !strings.Contains(joined, filepath.Join(home, ".local", "share", "Block", "goose")) {
		t.Errorf("the legacy Block/goose location is not a candidate: %v", dirs)
	}
	if runtime.GOOS == "darwin" {
		apple := filepath.Join(home, "Library", "Application Support", "Block", "goose")
		if dirs[0] != apple {
			t.Errorf("first candidate = %q, want goose's own macOS location %q", dirs[0], apple)
		}
		if !strings.Contains(joined, filepath.Join(home, "Library", "Application Support", "goose")) {
			t.Errorf("the location without the Block segment is not a candidate: %v", dirs)
		}
	}
	if runtime.GOOS == "linux" && dirs[0] != xdg {
		t.Errorf("first candidate = %q, want %q on linux", dirs[0], xdg)
	}

	// GOOSE_PATH_ROOT is the whole answer when it is set: goose puts config,
	// data and state under it and nothing stays behind.
	t.Setenv("GOOSE_PATH_ROOT", filepath.Join(home, "elsewhere"))
	if got := GooseDataDirs(); len(got) != 1 || got[0] != filepath.Join(home, "elsewhere", "data") {
		t.Errorf("with GOOSE_PATH_ROOT set, candidates = %v", got)
	}
}

// A store in any candidate root is read, and a store in two is read from both.
func TestGooseReadsEveryRootThatExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_GOOSE_ROOT", "")
	t.Setenv("DEJA_GOOSE_DB", "")

	dirs := GooseDataDirs()
	// A legacy JSONL session in the last candidate — the one a single-root
	// reader on macOS would never have looked at.
	legacy := filepath.Join(dirs[len(dirs)-1], "sessions")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"id":"s-legacy","working_dir":"/work/api","description":"why the migration hangs",` +
		`"message_count":1,"created":"2026-07-17T09:00:00Z"}` + "\n" +
		`{"role":"user","created":1784278800,"content":[{"type":"text","text":"why does the migration hang"}]}` + "\n"
	if err := os.WriteFile(filepath.Join(legacy, "s-legacy.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	files := GooseSessionFiles()
	if len(files) != 1 || !strings.HasSuffix(files[0], "s-legacy.jsonl") {
		t.Fatalf("a session in a non-primary root is not a candidate: %v", files)
	}
	if n := len(LoadGoose()); n != 1 {
		t.Fatalf("sessions = %d, want the one in the non-primary root", n)
	}
	// And the file is claimed by the registry kind, so incremental ingest reads
	// it rather than treating it as unknown content.
	kind := ""
	for _, h := range Registry() {
		if h.Name != "goose" {
			continue
		}
		for _, k := range h.Kinds {
			if k.Match(files[0]) {
				kind = k.Name
			}
		}
	}
	if kind != "goose-jsonl" {
		t.Errorf("registry kind for %s = %q, want goose-jsonl", files[0], kind)
	}
}
