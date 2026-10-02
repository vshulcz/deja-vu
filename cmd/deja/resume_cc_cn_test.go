package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func writeResumeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// `cmd --resume <id>` looks the id up under the project folder of the current
// directory only, so the command runs where the session did (#4372). The
// directory is the cwd on the v3 header line; the folder name is a lossy slug.
func TestResumeCommandCodeRunsInTheSessionDirectory(t *testing.T) {
	proj := t.TempDir()
	id := "1583a430-5538-475d-9190-0d726c7a374f"
	transcript := filepath.Join(t.TempDir(), "projects", "private-tmp-proj-cc", id+".jsonl")
	cwd, _ := json.Marshal(proj)
	writeResumeFile(t, transcript,
		`{"type":"session","version":3,"id":"`+id+`","timestamp":"2026-10-01T20:09:14.839Z","cwd":`+string(cwd)+`}`+"\n"+
			`{"type":"message","id":"f82f93b8","parentId":null,"message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`+"\n")

	bin := "cmd"
	if runtime.GOOS == "windows" {
		bin = "cmdc"
	}
	dir, cmd, err := resumeCommand(model.Session{Harness: "commandcode", ID: id, Path: transcript})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin+" --resume "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the header's cwd %q", dir, proj)
	}

	// The older flat shape records no directory: `--session`, which finds the
	// id in any project, without a cd (#4460).
	old := filepath.Join(filepath.Dir(transcript), "aaaa1111.jsonl")
	writeResumeFile(t, old, `{"role":"user","content":"fix the retry loop","timestamp":"2026-09-16T12:00:05Z","sessionId":"aaaa1111"}`+"\n")
	dir, cmd, err = resumeCommand(model.Session{Harness: "commandcode", ID: "aaaa1111", Path: old})
	if err != nil {
		t.Fatal(err)
	}
	if dir != "" || cmd != bin+" --session aaaa1111" {
		t.Errorf("v2 session: got (%q, %q)", dir, cmd)
	}

	// A directory that is gone gets no cd that would fail before cmd starts.
	if err := os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	if dir, cmd, err := resumeCommand(model.Session{Harness: "commandcode", ID: id, Path: transcript}); err != nil || dir != "" || cmd != bin+" --session "+id {
		t.Errorf("got (%q, %q, %v) for a directory that is gone, want %s --session with no cd", dir, cmd, err, bin)
	}
}

// `cn --fork <id>` loads the history from anywhere and runs its tools in the
// current directory, so the fork runs in the session's workspace (#4375).
func TestResumeContinueForksInTheWorkspace(t *testing.T) {
	proj := t.TempDir()
	sessions := filepath.Join(t.TempDir(), "sessions")
	id := "5f38800d-4bfe-4a34-a6f1-b117d8ce618c"
	ws, _ := json.Marshal(proj)
	path := filepath.Join(sessions, id+".json")
	writeResumeFile(t, path, `{"sessionId":"`+id+`","title":"retry","workspaceDirectory":`+string(ws)+`,"history":[]}`)

	dir, cmd, err := resumeCommand(model.Session{Harness: "continue", ID: id, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "cn --fork "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the workspace %q", dir, proj)
	}

	// The IDE extension records the workspace as a file URI, and an older
	// session file leaves it to sessions.json.
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(proj)}).String()
	if runtime.GOOS == "windows" {
		uri = (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(proj)}).String()
	}
	list, _ := json.Marshal([]map[string]string{{"sessionId": id, "workspaceDirectory": uri}})
	writeResumeFile(t, filepath.Join(sessions, "sessions.json"), string(list))
	writeResumeFile(t, path, `{"sessionId":"`+id+`","history":[]}`)
	if dir, _, _ := resumeCommand(model.Session{Harness: "continue", ID: id, Path: path}); filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q from a file URI in sessions.json, want %q", dir, proj)
	}

	// Gone: no cd.
	if err := os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(model.Session{Harness: "continue", ID: id, Path: path}); dir != "" {
		t.Errorf("dir = %q for a workspace that is gone", dir)
	}
}
