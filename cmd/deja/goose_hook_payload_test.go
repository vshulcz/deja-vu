package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The goose hooks are the two doors #2183's guard cannot see, because they live
// beside the installer rather than in `hook_*.go`. Both threw the payload away
// and recalled from wherever the process stood, so a host that runs its hooks
// from a plugin directory rather than the project got an empty file and no
// reason why. Goose's own payload fields are not documented to include cwd, so
// this is the door being right about a field it is handed, not a fix that
// changes goose's behaviour today (#2187).
func TestTheGooseHooksRecallForTheProjectTheirPayloadNames(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "beta1", []string{
		`{"type":"user","sessionId":"beta1","timestamp":"` + at +
			`","message":{"role":"user","content":"the beta work: the kafka consumer keeps rebalancing"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	beta := filepath.Join(base, "tmp", "beta")
	elsewhere := filepath.Join(base, "plugin-dir")
	for _, d := range []string{beta, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	moim := filepath.Join(base, "moim.md")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	recalled := func() string {
		b, err := os.ReadFile(moim)
		if err != nil {
			return ""
		}
		return string(b)
	}

	// The premise: standing in the project, the session-start hook recalls it.
	t.Chdir(beta)
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "s", "cwd": beta}))
	if err := cmdGooseHook("", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recalled(), "rebalancing") {
		t.Fatalf("the hook recalled nothing from inside the project, so this measures nothing:\n%s", recalled())
	}
	if err := os.Remove(moim); err != nil {
		t.Fatal(err)
	}

	// The case: the hook runs where the host put it, and the payload is the
	// only thing that names the project.
	t.Chdir(elsewhere)
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "s", "cwd": beta}))
	if err := cmdGooseHook("", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recalled(), "rebalancing") {
		t.Errorf("the session-start hook ignored the project its payload named:\n%s", recalled())
	}

	// And the prompt half, which re-encodes a payload of its own and used to
	// drop the project on the way.
	if err := os.Remove(moim); err != nil {
		t.Fatal(err)
	}
	payload := hookPayload(t, map[string]string{
		"session_id": "s", "cwd": beta, "message": "why does the kafka consumer keep rebalancing",
	})
	if err := refreshGooseForPrompt(index.DefaultDir(), []byte(payload)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recalled(), "rebalanc") {
		t.Errorf("the prompt hook ignored the project its payload named:\n%s", recalled())
	}
}
