package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// While a build runs the digest is empty, and a machine with environment facts
// took an early return that never reached the "a build is running" branch: the
// whole post-upgrade rebuild passed in silence on exactly the stores where it
// takes longest (#927).
func TestHookReportsTheBuildEvenWhenItHasOnlyEnvironmentFacts(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One directory per project: a wall of one repository is no longer a fact
	// about the machine, and this block is built from other projects' walls.
	var stores []string
	for i := 0; i < environmentMinProjects; i++ {
		store := filepath.Join(root, "-elsewhere"+strconv.Itoa(i))
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		stores = append(stores, store)
	}
	// Sessions from another project, each hitting the same missing tool: that
	// is what the environment block is built from, and none of it is this
	// project's memory.
	var b strings.Builder
	for i := 0; i < 4; i++ {
		id := "env" + strconv.Itoa(i)
		b.Reset()
		b.WriteString(`{"type":"user","message":{"role":"user","content":"run the thing"},"timestamp":"2026-07-0` + strconv.Itoa(i+1) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/elsewhere` + strconv.Itoa(i%environmentMinProjects) + `"}` + "\n")
		// Claude files tool results inside user messages, and friction is
		// counted from tool output only.
		b.WriteString(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"zsh:1: command not found: shellcheck"}]},"timestamp":"2026-07-0` + strconv.Itoa(i+1) + `T10:01:00Z","sessionId":"` + id + `","cwd":"/elsewhere` + strconv.Itoa(i%environmentMinProjects) + `"}` + "\n")
		if err := os.WriteFile(filepath.Join(stores[i%len(stores)], id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// A build asked for just now, nothing published yet — the first session
	// after an upgrade.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		SystemMessage string `json:"systemMessage"`
		Hook          struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("hook output is not JSON: %q (%v)", out, err)
	}
	if !strings.Contains(resp.Hook.AdditionalContext, "command not found: shellcheck") {
		t.Fatalf("this is not the environment-facts path:\n%s", resp.Hook.AdditionalContext)
	}
	if !strings.Contains(resp.SystemMessage, "indexing your history") {
		t.Errorf("the rebuild passed in silence: %q", resp.SystemMessage)
	}
}
