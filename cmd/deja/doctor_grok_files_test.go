package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Grok store keeps nine files of its own beside each transcript and eleven
// configuration files at the root; doctor counted every one of them as a
// transcript it could not read — 94 of them on a store with 11 sessions
// (#3319). The row's job is to say when a transcript went unread, and a number
// that size says the opposite.
func TestDoctorDoesNotCallGroksOwnFilesUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "grok")
	dir := filepath.Join(root, "sessions", "%2Fw%2Fapi", "6f0a1b2c-0000-4000-8000-000000000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", root)

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "updates.jsonl"),
		`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"why does the retry loop drop the last attempt"}}`+"\n")
	write(filepath.Join(dir, "summary.json"), `{"info":{"id":"6f0a1b2c-0000-4000-8000-000000000001"}}`)
	for _, name := range []string{
		"chat_history.jsonl", "events.jsonl", "rewind_points.jsonl", "prompt_context.json",
		"announcement_state.json", "signals.json", "resources_state.json",
	} {
		write(filepath.Join(dir, name), "{}\n")
	}
	write(filepath.Join(root, "sessions", "%2Fw%2Fapi", "prompt_history.jsonl"), "{}\n")
	for _, name := range []string{"settings.json", "user-settings.json", "auth.json", "models_cache.json"} {
		write(filepath.Join(root, name), "{}")
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	var row string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "grok") && strings.Contains(l, "file") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatal("no grok file row in the report at all")
	}
	if strings.Contains(row, "not recognised") {
		t.Errorf("row = %q, want no such note — every file beside the transcript is Grok's own", row)
	}

	// And the note is still there for the thing it exists for: a transcript
	// under a name the reader does not take.
	write(filepath.Join(dir, "updates.v2.jsonl"), "{}\n")
	buf.Reset()
	doctorHarnesses(&buf, t.TempDir())
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "grok") && strings.Contains(l, "file") {
			if !strings.Contains(l, "1 not recognised here") {
				t.Errorf("row = %q, want it to name the one file deja did not read", l)
			}
			break
		}
	}
}

// The row and the JSON name the same place. doctorStoreChecks keeps its own
// path per harness, and leaving grok's at the store root made `doctor --json`
// say ~/.grok where the row says ~/.grok/sessions — and, on a store deja is
// not allowed to read, turned "denied" into "missing" (review of #3319).
func TestDoctorJSONNamesTheSameGrokPathAsTheRow(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "grok")
	dir := filepath.Join(root, "sessions", "%2Fw%2Fapi", "6f0a1b2c-0000-4000-8000-000000000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", root)
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"),
		[]byte(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"why does the retry loop drop the last attempt"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "sessions")
	for _, c := range doctorStoreChecks() {
		if c.name != "grok" {
			continue
		}
		if len(c.paths) != 1 || c.paths[0] != want {
			t.Fatalf("grok paths = %v, want %q — the row says that one", c.paths, want)
		}
		return
	}
	t.Fatal("no grok entry in doctorStoreChecks at all")
}
