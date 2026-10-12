package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Codex edits through apply_patch, so a Codex session read back showed none
// of the code it changed. The patch is read the way Codex applies it: a file
// added whole, a file updated hunk by hunk, a rename, a deletion skipped, and
// the same text passed through an older build's shell call.
func TestChangesReadsACodexSessionsPatches(t *testing.T) {
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: /work/app/pkg/a.go",
		"@@ func retry()",
		" \tfor i := 0; i < n; i++ {",
		"-\t\tif err == nil {",
		"+\t\tif err == nil || i == max {",
		" \t\t\tbreak",
		"*** Add File: pkg/new.go",
		"+package pkg",
		"+",
		"+const max = 5",
		"*** Delete File: pkg/old.go",
		"*** Update File: pkg/b.go",
		"*** Move to: pkg/c.go",
		"@@",
		"-one",
		"+uno",
		"*** End Patch",
		"",
	}, "\n")
	shellArgs, _ := json.Marshal(map[string]any{"command": []string{"apply_patch", "*** Begin Patch\n*** Update File: pkg/d.go\n@@\n-x\n+y\n*** End Patch\n"}})
	rec := func(typ string, payload any) string {
		b, _ := json.Marshal(map[string]any{"type": typ, "payload": payload})
		return string(b)
	}
	lines := []string{
		rec("session_meta", map[string]any{"id": "s", "cwd": "/work/app"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "input": patch}),
		rec("response_item", map[string]any{"type": "function_call", "name": "shell", "arguments": string(shellArgs)}),
		rec("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "I would apply *** Begin Patch\n*** Add File: talk.go\n+no"}}}),
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range Changes(model.Session{Harness: "codex", ID: "s", Path: path}) {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New+"|"+map[bool]string{true: "new"}[c.Created])
	}
	want := []string{
		"pkg/a.go|\tfor i := 0; i < n; i++ {\n\t\tif err == nil {\n\t\t\tbreak|\tfor i := 0; i < n; i++ {\n\t\tif err == nil || i == max {\n\t\t\tbreak|",
		"pkg/new.go||package pkg\n\nconst max = 5|new",
		"pkg/c.go|one|uno|",
		"pkg/d.go|x|y|",
	}
	if strings.Join(got, "\n--\n") != strings.Join(want, "\n--\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n--\n"), strings.Join(want, "\n--\n"))
	}
}

// Two patches in one string are both read, and a fork's inherited
// session_meta does not move the directory its paths are read against.
func TestChangesReadsEveryCodexPatchAgainstTheFilesOwnDirectory(t *testing.T) {
	rec := func(typ string, payload any) string {
		b, _ := json.Marshal(map[string]any{"type": typ, "payload": payload})
		return string(b)
	}
	two := "*** Begin Patch\n*** Update File: /w/child/a.go\n@@\n-1\n+2\n*** End Patch\n*** Begin Patch\n*** Update File: b.go\n@@\n-3\n+4\n*** End Patch\n"
	lines := []string{
		rec("session_meta", map[string]any{"id": "child", "cwd": "/w/child", "forked_from_id": "parent"}),
		rec("session_meta", map[string]any{"id": "parent", "cwd": "/w/parent"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "input": two}),
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range Changes(model.Session{Harness: "codex", ID: "child", Path: path}) {
		got = append(got, c.Path+":"+c.New)
	}
	if strings.Join(got, " ") != "a.go:2 b.go:4" {
		t.Fatalf("got %v", got)
	}
}
