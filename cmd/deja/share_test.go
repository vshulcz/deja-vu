package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// promote writes a file with --to; share takes no flags and dropped them, so
// `deja share <id> --to out.md` printed to the terminal, wrote nothing, and
// said neither (#1002).
func TestShareRefusesFlagsItCannotHonour(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "out.md")
	_, err := captureRun(t, "share", "loc", "--to", out)
	if err == nil {
		t.Fatalf("share accepted a flag it does not implement, and wrote nothing")
	}
	if !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "promote") {
		t.Errorf("the refusal does not say what to use instead: %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("share wrote a file after refusing")
	}

	// The ordinary share is untouched.
	got, err := captureRun(t, "share", "loc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "deja share: loc") {
		t.Errorf("plain share stopped working:\n%s", got)
	}
}
