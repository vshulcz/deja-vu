package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// DEJA_RECALL=off is the kill switch. The digest honours it; the environment
// block went around it, because the "nothing to recall" branch builds that
// block without consulting the switch — so every session start still received
// text drawn from the indexed sessions the switch was set to keep out (#2699).
func TestRecallOffInjectsNothingAtAll(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-elsewhere")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Four sessions hitting the same missing tool: that is what the block is
	// built from, and none of it is this project's memory.
	for i := 1; i <= 4; i++ {
		id := "e" + strconv.Itoa(i)
		body := `{"type":"user","message":{"role":"user","content":"run the thing"},` +
			`"timestamp":"2026-07-0` + strconv.Itoa(i) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/elsewhere"}` + "\n" +
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result",` +
			`"content":"zsh:1: command not found: zonkotool"}]},` +
			`"timestamp":"2026-07-0` + strconv.Itoa(i) + `T10:01:00Z","sessionId":"` + id + `","cwd":"/elsewhere"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/nowhere")
	// hermeticEnv leaves DEJA_RECALL alone, and a developer who exports it
	// would fail the first leg rather than the one that matters.
	t.Setenv("DEJA_RECALL", "")

	// Without the switch, this machine's walls reach the session.
	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "zonkotool") {
		t.Fatalf("the fixture never produced an environment block, so the test proves nothing:\n%s", out)
	}

	t.Setenv("DEJA_RECALL", "off")
	out, err = captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "zonkotool") {
		t.Errorf("the kill switch was set and the session still got the environment block:\n%s", out)
	}
	if strings.Contains(out, "deja-recall") {
		t.Errorf("the kill switch was set and something was still injected:\n%s", out)
	}
}
