package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The cline row printed a count and nothing else, so a transcript deja could
// not read was invisible there while every other file-listing harness named it
// (#3360). A session directory keeps `<id>.json` beside `<id>.messages.json` —
// the manifest the reader opens itself for the title, the working directory
// and the timestamps — and that is not a failure either.
func TestDoctorNamesAnUnreadClineTranscriptButNotItsManifest(t *testing.T) {
	tmp := hermeticEnv(t)
	sessions := filepath.Join(tmp, "cline", "data", "sessions")
	dir := filepath.Join(sessions, "1757000000_ab12c")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOT", sessions)

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1757000000_ab12c.messages.json", `[{"role":"user","content":[{"type":"text","text":"why does the retry loop drop the last attempt"}]}]`)
	write("1757000000_ab12c.json", `{"session_id":"1757000000_ab12c","cwd":"/w/api","metadata":{"title":"retry loop"}}`)

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "cline") && strings.Contains(l, "file") {
				return l
			}
		}
		t.Fatal("no cline file row in the report at all")
		return ""
	}
	if got := row(); strings.Contains(got, "not recognised") {
		t.Errorf("row = %q, want no such note — the manifest is the reader's own", got)
	}
	// And the note is there for the thing it exists for: a transcript under a
	// name the reader does not take, which is what a format change looks like.
	write("conversation.json", `[{"role":"user","content":[{"type":"text","text":"anything"}]}]`)
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the transcript deja did not read", got)
	}
}

// The row names the legacy VS Code roots on the same line, so they are walked
// too: a stray file under a legacy task was invisible while the line said the
// root was covered (review of #3360).
func TestDoctorCountsAStrayFileUnderAClineLegacyRoot(t *testing.T) {
	tmp := hermeticEnv(t)
	legacy := filepath.Join(tmp, "vscode", "globalStorage", "saoudrizwan.claude-dev")
	task := filepath.Join(legacy, "tasks", "1757000000")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOTS", legacy)
	t.Setenv("DEJA_CLINE_ROOT", filepath.Join(tmp, "cline", "data", "sessions"))

	if err := os.WriteFile(filepath.Join(task, "api_conversation_history.json"),
		[]byte(`[{"role":"user","content":[{"type":"text","text":"why does the retry loop drop the last attempt"}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task, "ui_messages.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "cline") && strings.Contains(l, "file") {
			if !strings.Contains(l, "1 not recognised here") {
				t.Errorf("row = %q, want the stray file under the legacy root counted", l)
			}
			return
		}
	}
	t.Fatal("no cline file row in the report at all")
}
