package sources

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePiSession(t *testing.T, root, id, cwd string) string {
	t.Helper()
	dir := filepath.Join(root, "--w-proj--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-10-07T09-00-00-000Z_"+id+".jsonl")
	header := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T09:00:00.000Z"`
	if cwd != "" {
		header += `,"cwd":"` + cwd + `"`
	}
	lines := []string{
		header + `}`,
		`{"type":"message","id":"m1","timestamp":"2026-10-07T09:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}]}}`,
		`{"type":"message","id":"m2","timestamp":"2026-10-07T09:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"go test ./parser/..."}}]}}`,
		`{"type":"message","id":"m3","timestamp":"2026-10-07T09:00:03.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"--- FAIL: TestParseSeed\nwant 3, got 4"}],"isError":true}}`,
		`{"type":"compaction","id":"k1","timestamp":"2026-10-07T09:00:04.000Z","summary":"working on the parser","firstKeptEntryId":"m3"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The registry finds the reader for the file the host names, and the session
// is the one that reader returns under the host's id.
func TestCompactionSessionReadsTheNamedFileWithItsOwnReader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "pi")
	t.Setenv("DEJA_PI_ROOT", root)
	path := writePiSession(t, root, "pi-1", "/w/proj")
	tr, err := ReadCompactionSession("", "pi-1", path, "")
	if err != nil {
		t.Fatal(err)
	}
	text := storeSessionText(tr)
	if tr.Harness != "pi" || tr.Workspace != "/w/proj" || tr.Path != path || !strings.Contains(text, "go test ./parser/...") || !strings.Contains(text, "want 3, got 4") {
		t.Fatalf("harness %q workspace %q path %q\n%s", tr.Harness, tr.Workspace, tr.Path, text)
	}
	if _, err := ReadCompactionSession("", "pi-2", path, ""); !errors.Is(err, ErrTranscriptIdentity) {
		t.Fatalf("another session's file: %v", err)
	}
	if _, err := ReadCompactionSession("kimi", "pi-1", path, ""); err == nil {
		t.Fatal("a file read under a harness it does not belong to")
	}
}

// A host that names only the session and its harness: the store's file that
// names the id is the one read.
func TestCompactionSessionFindsTheSessionByID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "pi")
	t.Setenv("DEJA_PI_ROOT", root)
	writePiSession(t, root, "pi-other", "/w/other")
	path := writePiSession(t, root, "pi-1", "/w/proj")
	tr, err := ReadCompactionSession("pi", "pi-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Path != path || tr.Session.ID != "pi-1" {
		t.Fatalf("read %s as %s", tr.Path, tr.Session.ID)
	}
	if _, err := ReadCompactionSession("pi", "pi-missing", "", ""); err == nil {
		t.Fatal("a session the store does not hold was captured")
	}
	if _, err := ReadCompactionSession("", "pi-1", "", ""); !errors.Is(err, ErrUnsupportedCompactionTranscript) {
		t.Fatalf("neither file nor harness: %v", err)
	}
}

// A session that records no directory takes the hook's, but only when the
// project it was filed under is that directory's.
func TestCompactionSessionWorkspaceHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "pi")
	t.Setenv("DEJA_PI_ROOT", root)
	path := writePiSession(t, root, "pi-1", "")
	tr, err := ReadCompactionSession("pi", "pi-1", path, "/elsewhere/entirely")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Workspace != "" {
		t.Fatalf("took an unrelated hint: %q", tr.Workspace)
	}
	hint := "/x/" + strings.TrimPrefix(tr.Session.Project, "/")
	if tr, _ := ReadCompactionSession("pi", "pi-1", path, hint); tr.Workspace != hint {
		t.Fatalf("project %q, hint %q, workspace %q", tr.Session.Project, hint, tr.Workspace)
	}
}
