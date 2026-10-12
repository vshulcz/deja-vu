package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Changes reads a Claude Code transcript's edits back in order, relative to
// the directory the session ran in, every sub-edit of a MultiEdit included,
// and nothing from another session's lines in the same file.
func TestChangesReadsEditsBackFromTheTranscript(t *testing.T) {
	const id = "aaaa0001-1111-4000-8000-d6e7f8a9b0c1"
	lines := []string{
		`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/pkg/a.go","old_string":"x := 1","new_string":"x := 2"}}]}}`,
		`{"type":"assistant","sessionId":"other-session","cwd":"/work/app","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/pkg/b.go","old_string":"FOREIGN","new_string":"FOREIGN2"}}]}}`,
		`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","message":{"content":[{"type":"tool_use","name":"MultiEdit","input":{"file_path":"pkg/c.go","edits":[{"old_string":"one","new_string":"uno"},{"old_string":"two","new_string":"dos"}]}}]}}`,
		`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/etc/hosts.bak","content":"elsewhere"}}]}}`,
		// A transcript written on Windows reads the same anywhere.
		`{"type":"assistant","sessionId":"` + id + `","cwd":"C:\\work\\app","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"C:\\work\\app\\pkg\\d.go","old_string":"a","new_string":"b"}}]}}`,
		`not json`,
	}
	path := filepath.Join(t.TempDir(), id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Changes(model.Session{Harness: "claude", ID: id, Path: path})
	var paths []string
	for _, c := range got {
		paths = append(paths, c.Path+":"+c.New)
	}
	want := "pkg/a.go:x := 2|pkg/c.go:uno|pkg/c.go:dos|/etc/hosts.bak:elsewhere|pkg/d.go:b"
	if strings.Join(paths, "|") != want {
		t.Fatalf("got %v\nwant %s", paths, want)
	}
	// A file outside the project is not a change to it.
	if fin := FinalChanges(got); len(fin) != 4 {
		t.Fatalf("final %v", fin)
	}
	if Changes(model.Session{Harness: "gemini", Path: path}) != nil {
		t.Fatal("a harness whose edits it cannot read gives nothing, not a guess")
	}
}
