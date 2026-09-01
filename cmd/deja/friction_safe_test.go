package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A recorded error line is untrusted text — it came out of a transcript deja
// did not write. Every surface that prints one runs it through search.SafeLine
// first; friction was the exception, and printed the bytes as recorded.
//
// Found while reviewing #2895, whose JSON path inherited the same line. The
// terminal obeys what it is sent: an ANSI escape recolours the rest of the
// screen, and U+202E reverses the reading order of everything after it, so a
// command in a wall can be made to read as something else.
func TestFrictionDoesNotPrintWhatATerminalWouldObey(t *testing.T) {
	root := frictionEnv(t)
	writeEscapedFriction(t, root)

	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "shellcheck") {
		t.Fatalf("the wall was not reported at all, so this asserts nothing:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("an ANSI escape reached the terminal:\n%q", out)
	}
	if strings.Contains(out, "\u202e") {
		t.Errorf("a bidi override reached the terminal:\n%q", out)
	}
}

func writeEscapedFriction(t *testing.T, root string) {
	t.Helper()
	proj := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := "command not found: \x1b[31mshellcheck\x1b[0m\u202eevil"
	for i := 0; i < index.FrictionMinSessions; i++ {
		sid := fmt.Sprintf("esc%02d", i)
		payload, err := json.Marshal(bad + "\nexit status 127")
		if err != nil {
			t.Fatal(err)
		}
		row := `{"type":"user","sessionId":"` + sid + `","cwd":"/repo",` +
			`"timestamp":"2026-07-30T03:05:05Z","message":{"role":"user","content":` +
			`[{"type":"tool_result","content":` + string(payload) + `}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(row), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
