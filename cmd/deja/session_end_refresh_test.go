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
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	work := filepath.Join(tmp, "work")
	cache := hookCachePath(dir, work)
	if err := os.WriteFile(cache, []byte(`{"digest":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, payload string }{
		{"cursor", `{"conversation_id":"c-1","session_id":"c-1","hook_event_name":"sessionEnd","workspace_roots":["` + work + `"],"reason":"user_close"}`},
		{"claude", `{"session_id":"s-1","hook_event_name":"SessionEnd","cwd":"` + work + `","reason":"exit"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	work := filepath.Join(tmp, "work")
	payload := `{"session_id":"s-1","cwd":"` + work + `"}`

	runHookSessionEnd(dir, strings.NewReader(payload))
	if _, err := os.Stat(hookCachePath(dir, work) + ".refreshing"); err == nil {
		t.Error("refreshed a project with no cached digest")
	}

	if err := os.WriteFile(hookCachePath(dir, work), []byte(`{"digest":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_RECALL", "off")
	runHookSessionEnd(dir, strings.NewReader(payload))
	if _, err := os.Stat(hookCachePath(dir, work) + ".refreshing"); err == nil {
		t.Error("refreshed the digest with recall off")
	}
}
