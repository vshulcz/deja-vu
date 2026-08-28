package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// blame answers "who touched this file" with the ten best and said nothing
// about the rest, so ten of forty read as the whole answer — the sentence
// `search` prints two commands away (#2299).
func TestBlameSaysHowManyItHeldBack(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	for i := 0; i < 25; i++ {
		at := time.Now().Add(-time.Duration(2+i) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%03d","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"touching api/upload.go, take %d"}}`, i, at, i)
		name := fmt.Sprintf("s%03d.jsonl", i)
		if err := os.WriteFile(filepath.Join(claude, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: the cap really is holding sessions back.
	out, err := captureRun(t, "blame", "api/upload.go")
	if err != nil {
		t.Fatal(err)
	}
	capped := strings.Count(out, "touching api/upload.go")
	all, err := captureRun(t, "blame", "api/upload.go", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if whole := strings.Count(all, "touching api/upload.go"); whole <= capped {
		t.Fatalf("--all shows %d and the default %d, so nothing is being held back", whole, capped)
	}

	said, err := captureRunStderr(t, "blame", "api/upload.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "--all") {
		t.Errorf("blame held sessions back without saying so: %q", said)
	}
	if !strings.Contains(said, "25") {
		t.Errorf("the note does not say how many there are: %q", said)
	}

	// With --all there is nothing to announce.
	said, err = captureRunStderr(t, "blame", "api/upload.go", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(said, "add --all") {
		t.Errorf("--all still suggests --all: %q", said)
	}
}
