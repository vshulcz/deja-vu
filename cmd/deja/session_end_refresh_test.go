package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The start digest is served from a cache whose only refresh ran at the last
// session start, before that session wrote anything, so the session after it
// was handed a snapshot without it (#5119). The end of a session is where the
// cache goes stale, and the SessionEnd hook knows the project.
func TestSessionEndRefreshesTheProjectsStartDigest(t *testing.T) {
	requireNoSpawnHere(t)
	t.Setenv("DEJA_RECALL", "")
	t.Setenv("DEJA_HOOK_REFRESH", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("DEVIN_PROJECT_DIR", "")
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	work := filepath.Join(tmp, "work")
	esc := jsonEscaped(t, work)
	cache := hookCachePath(dir, work)
	if err := os.WriteFile(cache, []byte(`{"digest":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, payload, env string }{
		{"cursor", `{"conversation_id":"c-1","session_id":"c-1","hook_event_name":"sessionEnd","workspace_roots":["` + esc + `"],"reason":"user_close"}`, ""},
		{"claude", `{"session_id":"s-1","hook_event_name":"SessionEnd","cwd":"` + esc + `","reason":"exit"}`, ""},
		// Devin names the project only in its launcher's environment.
		{"devin", `{"session_id":"d-1","hook_event_name":"SessionEnd"}`, work},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEVIN_PROJECT_DIR", tc.env)
			_ = os.Remove(cache + ".refreshing")
			runHookSessionEnd(dir, strings.NewReader(tc.payload))
			if _, err := os.Stat(cache + ".refreshing"); err != nil {
				t.Errorf("the session ended and nothing refreshed the digest the next one reads: %v", err)
			}
		})
	}
}

// No cached digest means the next start builds one itself, recall off means
// nobody reads one, and a payload without a project names no cache.
func TestSessionEndLeavesNoCacheAlone(t *testing.T) {
	requireNoSpawnHere(t)
	t.Setenv("DEJA_RECALL", "")
	t.Setenv("DEJA_HOOK_REFRESH", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("DEVIN_PROJECT_DIR", "")
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	work := filepath.Join(tmp, "work")
	payload := `{"session_id":"s-1","cwd":"` + jsonEscaped(t, work) + `"}`
	sentinel := hookCachePath(dir, work) + ".refreshing"

	runHookSessionEnd(dir, strings.NewReader(payload))
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("refreshed a project with no cached digest")
	}

	if err := os.WriteFile(hookCachePath(dir, work), []byte(`{"digest":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The plugin shape: a session id and nothing about the project. The
	// process's own directory is not the project on every host, so no guess.
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	runHookSessionEnd(dir, strings.NewReader(`{"session_id":"s-1"}`))
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("refreshed a project the payload never named")
	}
	t.Setenv("DEJA_RECALL", "off")
	runHookSessionEnd(dir, strings.NewReader(payload))
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("refreshed the digest with recall off")
	}
}
