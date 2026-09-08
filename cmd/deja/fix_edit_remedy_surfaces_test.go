package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// editRemedyStore is the #2163 fixture: two sessions hit the same failing test
// and changed the same file after it.
func editRemedyStore(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i, id := range []string{"e1", "e2"} {
		at := time.Now().Add(-time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"the pool test keeps failing"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","id":"t` + id + `","input":{"command":"go test ./internal/pool"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t` + id + `","content":"--- FAIL: TestPoolDrainsOnClose (0.09s)"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","id":"x` + id + `","input":{"file_path":"/tmp/app/internal/pool.go","old_string":"close(c)","new_string":"c.drain()"}}]}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The edit remedy reached the terminal `fix` only: --json printed an empty
// command and no file, and the MCP fix mode printed "ran next:" with nothing
// after it (#3261).
func TestFixJSONCarriesTheEditRemedy(t *testing.T) {
	editRemedyStore(t)
	out, err := captureRun(t, "fix", "--json", "--- FAIL: TestPoolDrainsOnClose")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Fixes []struct {
			Command string `json:"command"`
			Edit    string `json:"edit"`
		} `json:"fixes"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if len(got.Fixes) == 0 {
		t.Fatalf("no fixes:\n%s", out)
	}
	if got.Fixes[0].Edit != "/tmp/app/internal/pool.go" || got.Fixes[0].Command != "" {
		t.Errorf("row = %+v, want the edited file and no command", got.Fixes[0])
	}
}

func TestMCPFixNamesTheFileThatChangedNext(t *testing.T) {
	dir := editRemedyStore(t)
	got, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"--- FAIL: TestPoolDrainsOnClose"}`))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if !strings.Contains(got, "changed next: /tmp/app/internal/pool.go") {
		t.Errorf("the answer does not name the file:\n%s", got)
	}
	if strings.Contains(got, "ran next") {
		t.Errorf("an edit was offered as a command to run:\n%s", got)
	}
}

// The path keeps its spacing (#2044): a file is named, not read aloud. The
// miner refuses paths with whitespace today, so this guards the renderer alone.
func TestFixJSONKeepsTheEditedPathVerbatim(t *testing.T) {
	var out strings.Builder
	if err := writeFixJSON(&out, []index.FixPair{{Error: "--- FAIL: TestX", Edit: "/tmp/app/two  spaces/pool.go"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"edit": "/tmp/app/two  spaces/pool.go"`) {
		t.Errorf("path not verbatim:\n%s", out.String())
	}
}
