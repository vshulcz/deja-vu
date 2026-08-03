package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repair rewrites the commands it manages, including a wrapper someone put
// there on purpose. It ran in silence, so the edit came back replaced with no
// word about it (#886).
func TestRewireNoteNamesWhatWasRewritten(t *testing.T) {
	if got := rewireNote(nil); got != "" {
		t.Errorf("a session that repaired nothing still said something: %q", got)
	}
	got := rewireNote([]string{"claude-auto", "codex"})
	for _, want := range []string{"claude-auto, codex", "`deja install` is what writes those commands"} {
		if !strings.Contains(got, want) {
			t.Errorf("note does not mention %q: %q", want, got)
		}
	}
	// The maintenance line goes ahead of the memory line, and neither is lost.
	joined := joinNotes(got, "deja: recalled 3 prior sessions")
	if !strings.HasPrefix(joined, got) || !strings.HasSuffix(joined, "recalled 3 prior sessions") {
		t.Errorf("joined note = %q", joined)
	}
	if joinNotes("", "only memory") != "only memory" || joinNotes("only maintenance", "") != "only maintenance" {
		t.Error("an empty half left a separator behind")
	}
}

// And it reaches the hook's output on the session where nothing else is being
// said — which is exactly the session that repaired the wiring.
func TestTheHookReportsAWiringRepairWithNothingElseToSay(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	// Never the real thing: on Windows a detached child holds deja.test.exe
	// open and `go test` fails to remove it after the run.
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	cfg := filepath.Join(tmp, "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "deja"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(wiringState{Version: "older", Targets: []string{"claude-auto"}, Exe: filepath.Join(tmp, "old-deja"), Home: os.Getenv("HOME")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rewrote its wiring for claude-auto") {
		t.Errorf("the session that rewrote the wiring said nothing:\n%s", out)
	}
}
