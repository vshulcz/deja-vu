package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// seedWalls lays down n sessions that each hit the same two errors.
func seedWalls(t *testing.T, n int) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("w%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/w","timestamp":"2026-07-2` +
			fmt.Sprint(i%10) + `T10:00:00Z","message":{"role":"user","content":"run the checks"}}` + "\n" +
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w/w","timestamp":"2026-07-2` +
			fmt.Sprint(i%10) + `T10:05:00Z","message":{"role":"user","content":[{"type":"tool_result",` +
			`"content":"zsh:1: command not found: shellcheck\nModuleNotFoundError: No module named 'yaml'"}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnvironmentBlockNamesWallsAndWhatToDo(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	got := environmentBlock(dir, policy.ActivationAuto)
	for _, want := range []string{
		"command not found: shellcheck",
		"No module named 'yaml'",
		fmt.Sprintf("%d separate sessions", index.FrictionMinSessions),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("block missing %q:\n%s", want, got)
		}
	}
	// Without an instruction the block is trivia the model skims past.
	if !strings.Contains(got, "alternative") {
		t.Errorf("block says nothing to do about it:\n%s", got)
	}
	// Three is what fits in a glance; a longer list is a nag.
	if n := strings.Count(got, "\n- "); n > environmentWalls {
		t.Errorf("named %d walls, cap is %d", n, environmentWalls)
	}
}

// Silence is the right answer on a machine that has not failed the same way
// three times: a block that fires on one-off errors is noise, and #582 is
// explicit that a noisy warning gets the feature turned off.
func TestEnvironmentBlockSilentBelowThreshold(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions-1)
	if got := environmentBlock(dir, policy.ActivationAuto); got != "" {
		t.Fatalf("want silence, got:\n%s", got)
	}
}

func TestEnvironmentReachesTheSessionStartInjection(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	// Through the hook itself, not the digest helper: a checkout with no
	// session of its own returns an empty digest, and the block must survive
	// that path — it is about the machine, not the project.
	var out bytes.Buffer
	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	hookErr := runHookContext(dir, true)
	os.Stdout = stdout
	_ = w.Close()
	_, _ = out.ReadFrom(r)
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if !strings.Contains(out.String(), "command not found: shellcheck") {
		t.Fatalf("the injection carries no environment block:\n%s", out.String())
	}
}

// The thirteen harnesses with a wired hook get the block at session start;
// everything else reaches deja through MCP, where the server process lives for
// the whole session — so it is served once and not on every recall.
func TestEnvironmentServedOncePerMCPSession(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	environmentServed = sync.Once{}
	first, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"checks","limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"checks","limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "This machine") {
		t.Fatalf("first recall carries no block:\n%s", first)
	}
	if strings.Contains(second, "This machine") {
		t.Fatalf("second recall repeats the block:\n%s", second)
	}
}

// Every other recall path runs its results through the policy table. This one
// read the manifest directly, so a user who denied a path still received error
// text drawn from the sessions that denial had just hidden (#659).
func TestEnvironmentBlockObeysThePolicy(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	if environmentBlock(dir, policy.ActivationMCP) == "" {
		t.Fatal("nothing to test: the block is empty before any policy is applied")
	}
	pol := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"mcp":{"local":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)
	if got := environmentBlock(dir, policy.ActivationMCP); got != "" {
		t.Fatalf("denied path still received machine facts drawn from hidden sessions:\n%s", got)
	}
	// A rule denying one path must not silence the others.
	if environmentBlock(dir, policy.ActivationAuto) == "" {
		t.Fatal("denying mcp also silenced auto")
	}
}

// The count is what a reader acts on, so a wall whose evidence is mostly
// hidden must not keep claiming the full number.
func TestEnvironmentBlockDropsWallsBelowThresholdAfterFiltering(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	pol := filepath.Join(t.TempDir(), "policy.json")
	// Deny every origin the seeded sessions could have.
	if err := os.WriteFile(pol, []byte(`{"activations":{"auto":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)
	if got := environmentBlock(dir, policy.ActivationAuto); got != "" {
		t.Fatalf("a wall survived with all its evidence denied:\n%s", got)
	}
}
