package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// What a session start actually emits when the store cannot answer. #800 pins
// the decision — both hooks ask for a rebuild and hand out nothing — at the
// digest function; this is the same session read the way a harness reads it,
// through the CLI envelope, because that notice is the only thing a person
// ever sees about a damaged store. doctor's wording (#2695) is a command they
// have no reason to run.
//
// The second half is the qualification: a warm cache is still the user's own
// history, so it is served alongside the notice rather than withheld (#874).
func TestASessionStartAgainstADamagedStoreIsQuietAndSaysWhy(t *testing.T) {
	tmp := hermeticEnv(t)
	// hermeticEnv suppresses the request outright, and the request is half of
	// what this pins: let it through and count the spawn instead of starting a
	// real child, the way the hook tests next door do.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	spawned := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })

	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"terraform apply --zonkoshard 7 failed"},` +
		`"timestamp":"2026-07-21T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if reason := index.DamageReason(dir); reason != "" {
		t.Fatalf("a freshly built store reported %q", reason)
	}
	if indexNeedsRebuild(dir) {
		t.Fatal("a freshly built store was counted as needing a rebuild")
	}

	// The postings go, which is the loss a partial copy or an over-eager
	// delete leaves: everything else still reads as a healthy index.
	if err := os.RemoveAll(filepath.Join(dir, "buckets")); err != nil {
		t.Fatal(err)
	}
	if !indexNeedsRebuild(dir) {
		t.Fatal("a store with no postings was not counted as needing a rebuild")
	}

	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")
	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseSessionStart(t, out)
	if !strings.Contains(payload.SystemMessage, "indexing your history") {
		t.Errorf("a damaged store passed in silence: %q", payload.SystemMessage)
	}
	// A store with no postings answers every term with nothing, and half a
	// memory presented as a whole one is worse than none.
	if payload.Hook.AdditionalContext != "" {
		t.Errorf("a damaged store handed out recall:\n%s", payload.Hook.AdditionalContext)
	}
	if spawned != 1 {
		t.Errorf("the session promised a rebuild and asked for %d", spawned)
	}

	// And with a cache to serve, the same session says both things at once.
	if err := os.Remove(filepath.Join(dir, "warmup.sentinel")); err != nil {
		t.Fatal(err)
	}
	writeHookCache(dir, "/proj", "an earlier digest", 1, 10, nil, 0, nil, nil)
	out, err = captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	payload = parseSessionStart(t, out)
	if !strings.Contains(payload.Hook.AdditionalContext, "an earlier digest") {
		t.Errorf("the user's own history was withheld because the store is damaged:\n%s", payload.Hook.AdditionalContext)
	}
	if !strings.Contains(payload.SystemMessage, "indexing your history") {
		t.Errorf("a cache hit swallowed the notice: %q", payload.SystemMessage)
	}
	if spawned != 2 {
		t.Errorf("a cache hit swallowed the rebuild request: %d spawns, want 2", spawned)
	}
}

type sessionStartOutput struct {
	SystemMessage string `json:"systemMessage"`
	Hook          struct {
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func parseSessionStart(t *testing.T, out string) sessionStartOutput {
	t.Helper()
	var p sessionStartOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &p); err != nil {
		t.Fatalf("the hook wrote something that is not its payload: %q", out)
	}
	return p
}
