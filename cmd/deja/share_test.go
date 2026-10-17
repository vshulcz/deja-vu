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
// said neither (#1002). A flag in front was taken as the id and indexed
// before the "no session matches" error (#5014).
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

	// The flag leads: same refusal, still no file, and before any lookup.
	_, err = captureRun(t, "share", "--to", out, "loc")
	if err == nil {
		t.Fatalf("share accepted a leading flag it does not implement, and wrote nothing")
	}
	if !strings.Contains(err.Error(), `unknown flag "--to"`) || !strings.Contains(err.Error(), "promote") {
		t.Errorf("the refusal does not say what to use instead: %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("share wrote a file after refusing a leading flag")
	}

	// A bare flag is not a session prefix (#5014).
	_, err = captureRun(t, "share", "--json")
	if err == nil || !strings.Contains(err.Error(), `unknown flag "--json"`) {
		t.Errorf("share --json was searched as a session prefix: %v", err)
	}

	// A second id is refused and named, not dropped (#5076).
	got, err := captureRun(t, "share", "loc", "extra")
	if err == nil || err.Error() != `share accepts one session id, got an extra "extra"` {
		t.Errorf("share with a second id: err=%v out=%q", err, got)
	}
	if got != "" {
		t.Errorf("share printed a digest before refusing a second id:\n%s", got)
	}

	// The ordinary share is untouched.
	got, err = captureRun(t, "share", "loc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "deja share: loc") {
		t.Errorf("plain share stopped working:\n%s", got)
	}
}
