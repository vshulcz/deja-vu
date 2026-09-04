package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The hook decoded stdin with no time bound, so a host that holds the pipe made
// every turn wait for it to close (#846). Bounding it alone would be worse than
// the wait: a payload deja could not read leaves invocationNum at 0, which the
// turn check reads as the first turn — the digest would go in before every
// model call rather than once.
func TestHookAntigravityBoundsStdinWithoutInjectingOnEveryTurn(t *testing.T) {
	hermeticEnv(t)
	// A store with something in it: with an empty one nothing is injected
	// whatever the payload says, and the check below would pass for the wrong
	// reason.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the anemometer mast bracket weld cracked"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"h1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")
	if d, _, _, _, _, _, _ := cachedHookDigest(dir); d == "" {
		t.Fatal("fixture has no digest to inject")
	}

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		var out bytes.Buffer
		_ = runHookAntigravity(dir, heldOpenReader{}, &out)
		done <- time.Since(start)
	}()
	select {
	case took := <-done:
		if took > 3*time.Second {
			t.Errorf("the hook waited %s on a held-open pipe", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the hook is still waiting for the host to close stdin")
	}

	for _, tc := range []struct {
		name    string
		payload string
		inject  bool
	}{
		{"unreadable payload", "not json at all", false},
		{"empty payload", "", false},
		{"seventh call", `{"invocationNum":7}`, false},
		// Zero is the first call, not one: the harness counts from there and
		// restarts every turn (measured on antigravity-cli 1.1.13).
		{"first call", `{"invocationNum":0}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := runHookAntigravity(dir, strings.NewReader(tc.payload), &out); err != nil {
				t.Fatal(err)
			}
			injected := strings.Contains(out.String(), "injectSteps")
			if injected != tc.inject {
				t.Errorf("%s: injected=%v, want %v — %s", tc.name, injected, tc.inject, out.String())
			}
		})
	}
}

// heldOpenReader never returns data and never reports EOF, like a host that
// keeps the pipe open for the life of the turn.
type heldOpenReader struct{}

func (heldOpenReader) Read([]byte) (int, error) {
	select {}
}

var _ io.Reader = heldOpenReader{}
