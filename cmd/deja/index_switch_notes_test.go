package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedFailThenFix writes two sessions where a test failed and the same command
// ran after it, and builds the index.
func seedFailThenFix(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "policy.json"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"s1", "s2"} {
		line := `{"type":"assistant","timestamp":"2026-07-01T10:00:00Z","sessionId":"` + sid + `","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n" +
			`{"type":"user","timestamp":"2026-07-01T10:00:05Z","sessionId":"` + sid + `","cwd":"/api","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"panic: runtime error: index out of range [3] with length 3"}]}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-07-01T10:01:00Z","sessionId":"` + sid + `","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go generate ./..."}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// On a search-only index `deja fix` says no fixes were mined, rather than that
// nothing on this machine ever followed the error (#5395).
func TestFixOnASearchOnlyIndexSaysWhy(t *testing.T) {
	const errLine = "panic: runtime error: index out of range [3] with length 3"
	// The control: the same store mined names the remedy.
	var out bytes.Buffer
	if err := runFix(seedFailThenFix(t), []string{errLine}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "go generate") {
		t.Fatalf("the mined store has no fix for the error, so this proves nothing: %q", out.String())
	}
	out.Reset()

	t.Setenv(index.SearchOnlyEnv, "1")
	dir := seedFailThenFix(t)
	if err := runFix(dir, []string{errLine}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "search-only") || strings.Contains(out.String(), "no session on this machine") {
		t.Errorf("deja fix on a search-only index: %q", out.String())
	}
	text, err := callMCPTool(dir, "fix", []byte(`{"error":"`+errLine+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "search-only") || strings.Contains(text, "No session on this machine") {
		t.Errorf("MCP fix on a search-only index: %q", text)
	}
}

// With the parse-level switch off, `deja how` names the switch rather than
// saying no command mentions the words.
func TestHowNamesTheSwitchThatKeptCommandsOut(t *testing.T) {
	var out bytes.Buffer
	if err := runHow(seedFailThenFix(t), []string{"go", "test"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "go test") || strings.Contains(out.String(), "DEJA_INDEX") {
		t.Fatalf("the control store does not list the command: %q", out.String())
	}
	out.Reset()

	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	dir := seedFailThenFix(t)
	if err := runHow(dir, []string{"go", "test"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "DEJA_INDEX_COMMANDS=0") {
		t.Errorf("deja how with commands off: %q", out.String())
	}
	text, err := callMCPTool(dir, "how", []byte(`{"what":"go test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "DEJA_INDEX_COMMANDS=0") {
		t.Errorf("MCP how with commands off: %q", text)
	}
}

// And `deja restore` names DEJA_INDEX_EDITS=0.
func TestRestoreNamesTheSwitchThatKeptEditsOut(t *testing.T) {
	t.Setenv("DEJA_INDEX_EDITS", "0")
	dir := seedFailThenFix(t)
	var out bytes.Buffer
	if err := runRestore(dir, []string{"main.go"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "DEJA_INDEX_EDITS=0") {
		t.Errorf("deja restore with edits off: %q", out.String())
	}
}
