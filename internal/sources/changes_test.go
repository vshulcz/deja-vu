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
	paths := make([]string, 0, len(got))
	for _, c := range got {
		paths = append(paths, c.Path+":"+c.New)
	}
	want := "pkg/a.go:x := 2|pkg/c.go:uno|pkg/c.go:dos|/etc/hosts.bak:elsewhere|pkg/d.go:b"
	if strings.Join(paths, "|") != want {
		t.Fatalf("got %v\nwant %s", paths, want)
	}
	// A file outside the project, or a scratch *_test.py created outside test
	// directories, is not a change to the project.
	got = append(got,
		Change{Path: "sub/foo_test.py", New: "assert True", Created: true},
		Change{Path: "tests/foo_test.py", Old: "1", New: "2"},
	)
	fin := FinalChanges(got)
	if len(fin) != 5 || fin[len(fin)-1].Path != "tests/foo_test.py" {
		t.Fatalf("final %v", fin)
	}
	if Changes(model.Session{Harness: "gemini", Path: path}) != nil {
		t.Fatal("a harness whose edits it cannot read gives nothing, not a guess")
	}

	// Antigravity CLI records edit_file, write_file, and write_to_file on
	// PLANNER_RESPONSE tool_calls with JSON-encoded or plain string arguments.
	agyLines := []string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","content":"hello"}`,
		`{"source":"MODEL","type":"GENERIC","content":"mentioned \"edit_file\" in prose"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/work/app"}},{"name":"read_file","args":{"Path":"/work/app/pkg/a.go"}},{"name":"edit_file","args":{"Path":"\"/work/app/pkg/a.go\"","Target":"\"x := 1\"","Replacement":"\"x := 2\""}},{"name":"write_file","args":{"Path":"/work/app/pkg/b.go","Content":"package pkg\n"}},{"name":"write_to_file","args":{"TargetFile":"file:///work/app/pkg/c.go","CodeContent":"package pkg // c\n"}}]}`,
	}
	agyPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(agyPath, []byte(strings.Join(agyLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agyGot := Changes(model.Session{Harness: "antigravity", ID: id, Path: agyPath})
	if len(agyGot) != 3 || agyGot[0].Path != "pkg/a.go" || agyGot[0].Old != "x := 1" || agyGot[0].New != "x := 2" || !agyGot[1].Created || agyGot[1].Path != "pkg/b.go" || !agyGot[2].Created || agyGot[2].Path != "pkg/c.go" {
		t.Fatalf("antigravity Changes = %+v", agyGot)
	}
	if Changes(model.Session{Harness: "antigravity", ID: id, Path: filepath.Join(t.TempDir(), "missing.jsonl")}) != nil {
		t.Fatal("missing antigravity transcript should return nil")
	}
}
