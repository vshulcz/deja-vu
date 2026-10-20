package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A --re miss told the reader to try --re, and listed the pattern's words as
// if they were looked up together (#5457).
func TestRegexMissAdviceFitsAPattern(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i, text := range []string{"the quokkabloom pool starves", "plinths are missing from the manifest", "the cache warms slowly"} {
		id := string(rune('a'+i)) + "1"
		line := `{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		q, want string
	}{
		{"^nosuchxyz$", "without ^ or $"},
		{"nosuch[^0-9]\\$", "try a looser pattern"},
		{"quokkabloom plinths", "try a looser pattern"},
	} {
		out, err := captureRunStderr(t, "--re", c.q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, c.want) || strings.Contains(out, "or --re") {
			t.Errorf("--re %q: %q, want %q", c.q, out, c.want)
		}
		if strings.Contains(out, "no session has them together") {
			t.Errorf("--re %q: per-word line on a pattern: %q", c.q, out)
		}
	}
	// Control: a plain miss keeps its advice and its per-word line.
	out, err := captureRunStderr(t, "quokkabloom plinths")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "try fewer words or --re") || !strings.Contains(out, "no session has them together") {
		t.Errorf("plain miss: %q", out)
	}
}
