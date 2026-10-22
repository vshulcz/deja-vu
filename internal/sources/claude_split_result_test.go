package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tail answering a call the last pass read is appended only when the answer
// changes nothing stored: a clean result stamps exit 0 on a command, so that
// one is read whole, and a clean answer to any other tool is not.
func TestClaudeTailAnsweringACallBeforeIt(t *testing.T) {
	call := func(id, name, input string) string {
		return `{"type":"assistant","sessionId":"s","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}` + "\n"
	}
	result := func(id string, failed bool, out string) string {
		e := "false"
		if failed {
			e = "true"
		}
		return `{"type":"user","sessionId":"s","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","is_error":` + e + `,"content":"` + out + `"}]}}` + "\n"
	}
	bash := call("c1", "Bash", `{"command":"go build ./..."}`)
	edit := call("c1", "Edit", `{"file_path":"/w/main.go","old_string":"a","new_string":"b"}`)
	far := bash + `{"type":"user","sessionId":"s","message":{"role":"user","content":"` + strings.Repeat("x", callWindow) + `"}}` + "\n"
	cases := []struct {
		name, head, tail string
		want             bool
	}{
		{"clean result of a command", bash, result("c1", false, "ok"), false},
		{"clean result of an edit", edit, result("c1", false, "updated"), true},
		{"failure with its code", bash, result("c1", true, "Exit code 2"), false},
		{"failure with no code", bash, result("c1", true, "boom"), true},
		{"call and result both in the tail", "", bash + result("c1", false, "ok"), true},
		{"call past the window", far, result("c1", false, "ok"), true},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, "s.jsonl")
			head := claudeIDLine("s", "one") + tc.head
			if err := os.WriteFile(p, []byte(head+tc.tail), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := claudeResumes(p, int64(len(head))); got != tc.want {
				t.Errorf("claudeResumes = %v, want %v", got, tc.want)
			}
		})
	}
}
