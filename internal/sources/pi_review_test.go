package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPiReviewToolVariants(t *testing.T) {
	for _, tc := range []struct {
		name, harness, tool string
		args                map[string]any
		file, old, written  string
		command             string
	}{
		{name: "openclaw exec", harness: "openclaw", tool: "exec", args: map[string]any{"command": "go test ./..."}, command: "$ go test ./..."},
		{name: "omp replace", harness: "omp", tool: "edit", args: map[string]any{"path": "src/main.go", "old_string": "before", "new_string": "after with enough detail for attribution"}, file: "src/main.go", old: "before", written: "after with enough detail for attribution"},
		{name: "omp read range", harness: "omp", tool: "read", args: map[string]any{"path": "src/main.go:10-20"}, file: "src/main.go"},
		{name: "omp read line", harness: "omp", tool: "read", args: map[string]any{"path": "src/main.go:10"}, file: "src/main.go"},
		{name: "omp open range", harness: "omp", tool: "read", args: map[string]any{"path": "src/main.go:10-"}, file: "src/main.go"},
		{name: "omp tail", harness: "omp", tool: "read", args: map[string]any{"path": "src/main.go:-10"}, file: "src/main.go"},
		{name: "pi string array", harness: "pi", tool: "edit", args: map[string]any{"path": "src/main.go", "edits": `[{"oldText":"before","newText":"after with enough detail for attribution"}]`}, file: "src/main.go", old: "before", written: "after with enough detail for attribution"},
		{name: "pi string object", harness: "pi", tool: "edit", args: map[string]any{"path": "src/main.go", "edits": `{"oldText":"before","newText":"after with enough detail for attribution"}`}, file: "src/main.go", old: "before", written: "after with enough detail for attribution"},
		{name: "pi object", harness: "pi", tool: "edit", args: map[string]any{"path": "src/main.go", "edits": map[string]any{"oldText": "before", "newText": "after with enough detail for attribution"}}, file: "src/main.go", old: "before", written: "after with enough detail for attribution"},
		{name: "malformed edits", harness: "pi", tool: "edit", args: map[string]any{"path": "src/main.go", "edits": "not JSON"}, file: "src/main.go"},
		{name: "non-list edits", harness: "pi", tool: "edit", args: map[string]any{"path": "src/main.go", "edits": "42"}, file: "src/main.go"},
		{name: "mixed edits", harness: "pi", tool: "edit", args: map[string]any{"path": "src/main.go", "edits": []any{nil, 42, map[string]any{"oldText": "before", "newText": "after with enough detail for attribution"}}}, file: "src/main.go", old: "before", written: "after with enough detail for attribution"},
		{name: "non-omp colon path", harness: "pi", tool: "read", args: map[string]any{"path": "src/main.go:10-20"}, file: "src/main.go:10-20"},
		{name: "omp literal colon", harness: "omp", tool: "read", args: map[string]any{"path": "src/main.go:notes"}, file: "src/main.go:notes"},
		{name: "omp hashline not claimed", harness: "omp", tool: "edit", args: map[string]any{"input": "*** Begin Patch\n*** Update File: src/main.go\n@@\n-before\n+after\n*** End Patch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			piToolsEnvironment(t)
			cwd := t.TempDir()
			header, err := json.Marshal(map[string]any{"type": "session", "id": "review", "cwd": cwd})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := json.Marshal(map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "name": tc.tool, "arguments": tc.args}}}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "review.jsonl")
			if err := os.WriteFile(path, append(append(header, '\n'), append(turn, '\n')...), 0600); err != nil {
				t.Fatal(err)
			}
			for _, offset := range []int64{0, int64(len(header) + 1)} {
				sessions, err := parsePiShaped(path, offset, tc.harness, "review", true)
				if err != nil {
					t.Fatal(err)
				}
				if tc.file == "" && tc.command == "" {
					if len(sessions) != 0 {
						t.Fatalf("unsupported patch produced records: %+v", sessions)
					}
					continue
				}
				if len(sessions) != 1 {
					t.Fatalf("sessions = %+v", sessions)
				}
				roles := piToolRoles(sessions[0].Messages)
				want := map[string][]string{}
				file := filepath.Join(cwd, tc.file)
				if tc.file != "" {
					want[RoleFiles] = []string{file}
				}
				if tc.old != "" {
					want[RoleEdit] = []string{file + "\n" + tc.old}
				}
				if tc.command != "" {
					want[RoleCommand] = []string{tc.command}
				}
				if tc.written != "" {
					want[RoleWrote] = []string{WroteRecord(file, tc.written)}
				}
				if !reflect.DeepEqual(roles, want) {
					t.Errorf("offset %d: records = %#v, want %#v", offset, roles, want)
				}
			}
		})
	}
}

func TestPiToolAbsoluteAndMissingCwd(t *testing.T) {
	piToolsEnvironment(t)
	absolute := filepath.Join(t.TempDir(), "absolute.go")
	for _, tc := range []struct{ cwd, path, want string }{
		{t.TempDir(), absolute, absolute},
		{"", "relative.go", "relative.go"},
		{t.TempDir(), "", ""},
		{t.TempDir(), "bad\npath", ""},
	} {
		raw := piToolCalls([]any{map[string]any{"type": "toolCall", "name": "read", "arguments": map[string]any{"path": tc.path}}}, tc.cwd, "pi")
		if got := claudeToolPaths(raw); got != tc.want {
			t.Errorf("path %q: got %q, want %q", tc.path, got, tc.want)
		}
	}
}
