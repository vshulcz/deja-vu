package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// The block used to close with "the tool or module is still missing" whenever
// it knew no remedy. Friction is any error three projects keep hitting, so that
// sentence was asserted over compile errors and connection timeouts too — the
// block this machine served listed `undefined: snorblefunc in vendor/blarg/api.go`
// under it, where nothing is missing and nothing can be installed.
func TestEnvironmentBlockDoesNotBlameAMissingTool(t *testing.T) {
	tmp := hermeticEnv(t)
	var roots []string
	for i := 0; i < environmentMinProjects; i++ {
		root := filepath.Join(tmp, "claude", fmt.Sprintf("proj-c%d", i))
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))

	// Two recurring errors that no install fixes: a compile failure and a
	// network timeout.
	for i := 0; i < index.FrictionMinSessions; i++ {
		sid := fmt.Sprintf("c%02d", i)
		cwd := fmt.Sprintf("/w/proj%d", i%environmentMinProjects)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"` + cwd +
			`","timestamp":"2026-07-2` + fmt.Sprint(i%10) + `T10:00:00Z","message":{"role":"user","content":"run the checks"}}` + "\n" +
			`{"type":"user","sessionId":"` + sid + `","cwd":"` + cwd +
			`","timestamp":"2026-07-2` + fmt.Sprint(i%10) + `T10:05:00Z","message":{"role":"user","content":[{"type":"tool_result",` +
			`"content":"undefined: widgetFunc in vendor/acme/api.go\nConnection timed out during banner exchange"}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(roots[i%len(roots)], sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	got := environmentBlock(dir, policy.ActivationAuto)
	// A test that passes by producing no block would pin nothing.
	if got == "" {
		t.Fatal("no block, so this test asserts nothing")
	}
	if !strings.Contains(got, "undefined: widgetFunc") {
		t.Fatalf("the compile error is not in the block:\n%s", got)
	}
	if strings.Contains(got, "the tool or module is still missing") {
		t.Errorf("the block blames a missing tool for errors no install fixes:\n%s", got)
	}
	if !strings.Contains(got, "nothing here has cleared them yet") {
		t.Errorf("the block does not say what is true of all of them:\n%s", got)
	}
	// The instruction has to survive the rewording: without it the block is
	// trivia the model skims past.
	if !strings.Contains(got, "alternative") {
		t.Errorf("block says nothing to do about it:\n%s", got)
	}
}
