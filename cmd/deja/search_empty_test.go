package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "Try fewer words" is advice for a different problem when a filter the caller
// set is what emptied the result — `deja last` has named it all along (#715).
func TestEmptySearchNamesTheFilter(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the connection pool starves"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRunStderr(t, "connection", "--since", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "matched nothing under since") {
		t.Errorf("--since: %q", out)
	}
	if strings.Contains(out, "try fewer words") {
		t.Errorf("--since still gave the generic advice: %q", out)
	}

	out, err = captureRunStderr(t, "connection", "--project", "nosuch")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `project "nosuch"`) {
		t.Errorf("--project: %q", out)
	}

	// An ordinary miss keeps the ordinary advice.
	out, err = captureRunStderr(t, "moria")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "try fewer words") {
		t.Errorf("plain miss: %q", out)
	}
}

// The hint from #674 was supposed to catch `stat` and `serch`, not every
// sentence starting with a short word (#715).
func TestCommandHintIgnoresShortFirstWords(t *testing.T) {
	for _, q := range []string{
		"a very long query that no session will ever contain",
		"a",
		"is the pool starving",
		"do we cap retries",
	} {
		if got := commandHint(q); got != "" {
			t.Errorf("%q produced %q", q, got)
		}
	}
	// The ones it exists for still work.
	for _, q := range []string{"stat", "serch pool", "shwo abc", "lasr"} {
		if got := commandHint(q); got == "" {
			t.Errorf("%q produced no hint", q)
		}
	}
}
