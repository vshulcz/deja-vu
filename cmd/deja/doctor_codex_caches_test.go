package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Codex store keeps its plugin cache, model cache, hooks and credentials
// beside the sessions directory; doctor walked the whole root and reported all
// of it as transcripts it could not read — 29 of them on this machine (#3321).
func TestDoctorDoesNotCallCodexCachesUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	day := filepath.Join(root, "sessions", "2026", "09", "07")
	if err := os.MkdirAll(filepath.Join(root, "plugins", "cache", "openai-templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", root)

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sid := "2f7c1b90-0000-4000-8000-000000000001"
	write(filepath.Join(day, "rollout-2026-09-07T10-00-00-"+sid+".jsonl"),
		`{"type":"session_meta","payload":{"id":"`+sid+`","timestamp":"2026-09-07T10:00:00Z","cwd":"/w/api"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"user_message","message":"why does the retry loop drop the last attempt"}}`+"\n")
	write(filepath.Join(root, "history.jsonl"), `{"session_id":"`+sid+`","text":"why does the retry loop drop the last attempt"}`+"\n")
	for _, name := range []string{"version.json", "models_cache.json", "hooks.json", "auth.json"} {
		write(filepath.Join(root, name), "{}")
	}
	write(filepath.Join(root, "plugins", "cache", "openai-templates", "manifest.json"), "{}")

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "codex") && strings.Contains(l, "file") {
				return l
			}
		}
		t.Fatal("no codex file row in the report at all")
		return ""
	}
	if got := row(); strings.Contains(got, "not recognised") {
		t.Errorf("row = %q, want no such note — the caches and the credentials are not transcripts", got)
	}
	// The note still does what it exists for: a rollout under a name the
	// reader does not take.
	write(filepath.Join(day, "rollout.v2.jsonl"), "{}\n")
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the one file in sessions deja did not read", got)
	}
}
