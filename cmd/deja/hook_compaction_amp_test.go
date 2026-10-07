package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Amp has no compaction event; its plugin finds one on agent.start and hands
// the turns before the cut to hook-precompact as a thread file, marked as a
// catch-up. Handed over again on the next agent.start, the same compaction is
// not captured or delivered twice.
func TestAmpCatchUpIsCapturedOnce(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	thread := filepath.Join(t.TempDir(), "deja-amp-1.json")
	// Amp writes file:///C:/work on Windows and file:///work elsewhere.
	uri := "file://" + filepath.ToSlash(workspace)
	if !strings.HasPrefix(filepath.ToSlash(workspace), "/") {
		uri = "file:///" + filepath.ToSlash(workspace)
	}
	body, _ := json.Marshal(map[string]any{
		"id": "T-1", "title": "", "created": 1791000000000,
		"env": map[string]any{"initial": map[string]any{"trees": []any{map[string]any{"uri": uri}}}},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the tokenizer test"}}},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "the tokenizer fails on escapes"}}},
		},
	})
	if err := os.WriteFile(thread, body, 0o644); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id": "T-1", "transcript_path": thread, "cwd": workspace, "harness": "amp", "deja_catch_up": true,
	})
	prompt, _ := json.Marshal(map[string]any{"prompt": "carry on", "session_id": "T-1", "cwd": workspace})
	ask := func() string {
		var out bytes.Buffer
		if err := runHookPromptMode(dir, bytes.NewReader(prompt), &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	withHookStdin(t, string(payload))
	runHookPrecompactFor(dir, "")
	out := ask()
	for _, want := range []string{"Compaction context", "fix the tokenizer test"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	withHookStdin(t, string(payload))
	runHookPrecompactFor(dir, "")
	if again := ask(); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same compaction was handed back twice: %s", again)
	}
}
