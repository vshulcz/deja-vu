package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The Roo and legacy Cline readers indexed the words of a turn and nothing it
// did, so those sessions carried no command record and no files record at all:
// `how`, `files`, `blame` and the fix-pair miner were blind to two harnesses
// while the modern Cline reader emitted all of them (#3295).
func TestRooTaskYieldsTheWorkItDid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "1767225700000")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	turns := []map[string]any{
		{"role": "user", "content": "the build fails on the widget parser"},
		{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "execute_command",
				"input": map[string]any{"command": "go test ./..."}},
		}},
		{"role": "user", "content": []any{
			map[string]any{"type": "tool_result",
				"content": "pkg/parser.go:42:9: undefined: frobnicateWidget\nExit code: 1"},
		}},
		{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "apply_diff",
				"input": map[string]any{"path": "pkg/parser.go", "diff": "<<<<<<< SEARCH"}},
		}},
	}
	b, err := json.Marshal(turns)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	// The legacy Cline store is the same file in the same shape — the two
	// harnesses share the extension both came from — so both readers are asked
	// the same question.
	for _, tc := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"cline (legacy)", ParseClineFile},
	} {
		t.Run(tc.name, func(t *testing.T) { assertRooWorkRecords(t, tc.parse, path) })
	}
}

func assertRooWorkRecords(t *testing.T, parse func(string) ([]model.Session, error), path string) {
	t.Helper()
	ss, err := parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	got := map[string][]string{}
	for _, m := range ss[0].Messages {
		got[m.Role] = append(got[m.Role], m.Text)
	}
	for _, want := range []struct{ role, text string }{
		// The `$ ` is deja's own marker on a command record, the same one the
		// Claude and Cline readers write.
		{RoleCommand, "$ go test ./..."},
		{RoleFiles, "pkg/parser.go"},
	} {
		found := false
		for _, have := range got[want.role] {
			if have == want.text {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s record for %q; got %v", want.role, want.text, got[want.role])
		}
	}
	// The error a command hit is what a later search reaches for, and the
	// fix-pair miner pairs it with what ran next.
	found := false
	for _, have := range got[RoleToolOutput] {
		if strings.Contains(have, "frobnicateWidget") {
			found = true
		}
	}
	if !found {
		t.Errorf("no tool output record carrying the error: %v", got[RoleToolOutput])
	}
}
