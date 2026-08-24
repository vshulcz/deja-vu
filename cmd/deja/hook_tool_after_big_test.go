package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// bigToolAfterPayload is a PostToolUse payload whose stderr begins with err and
// is padded past the stdin bound.
func bigToolAfterPayload(t *testing.T, err, cwd string, size int) []byte {
	t.Helper()
	obj := map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": "make deploy ENV=staging"},
		"tool_response":   map[string]string{"stderr": err + "\n"},
		"session_id":      "big",
		"cwd":             cwd,
	}
	base, _ := json.Marshal(obj)
	pad := size - len(base)
	if pad < 0 {
		pad = 0
	}
	obj["tool_response"] = map[string]string{"stderr": err + "\n" + strings.Repeat("x", pad)}
	b, merr := json.Marshal(obj)
	if merr != nil {
		t.Fatal(merr)
	}
	return b
}

// readHookPayload stops at 1 MiB, so a bigger payload is cut mid-string and the
// decode yields nothing — the hook then answers nothing, even though the error
// it knows is the first line of the output (#1716). A failing verbose build is
// exactly where this hook earns its keep.
func TestToolAfterUsesWhatArrivedFromAHugePayload(t *testing.T) {
	const knownErr = "panic: sql: database is closed"
	seedFixPair(t, knownErr, "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")

	var small, big bytes.Buffer
	if err := runHookToolAfter(dir, bytes.NewReader(bigToolAfterPayload(t, knownErr, "/work/app", 900<<10)), &small); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(small.String(), "what followed it") {
		t.Fatalf("the control did not recall at all, fixture is wrong:\n%s", small.String())
	}
	if err := runHookToolAfter(dir, bytes.NewReader(bigToolAfterPayload(t, knownErr, "/work/app", 1200<<10)), &big); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(big.String(), "what followed it") {
		t.Errorf("a payload over the stdin bound lost the recall:\n%q", big.String())
	}
}

// A salvaged value is raw JSON text, so the escapes have to come back out
// correctly — an escaped backslash must not be re-read as the start of the next
// escape, and a value cut mid-escape must not swallow the byte after it.
func TestSalvagedOutputUnescapes(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{`{"stderr":"a\nb"}`, "a\nb"},
		{`{"stderr": "spaced"}`, "spaced"},
		{`{"stderr":"C:\\src\\n"}`, `C:\src\n`},
		{`{"stderr":"say \"hi\""}`, `say "hi"`},
		{`{"stderr":"cut off here, no end quote`, "cut off here, no end quote"},
		{`{"stdout":"","stderr":"second key wins"}`, "second key wins"},
	} {
		if got := salvageToolOutput(tc.raw); got != tc.want {
			t.Errorf("salvageToolOutput(%s) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// Letting a cut payload past the tool-name gate must not let every tool past
// it: the name is read out of the raw bytes, and a read of someone else's file
// is not a command that failed.
func TestToolAfterStillIgnoresOtherToolsWhenCut(t *testing.T) {
	const knownErr = "panic: sql: database is closed"
	seedFixPair(t, knownErr, "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")

	obj := map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Read",
		"tool_input":      map[string]string{"file_path": "/work/app/log.txt"},
		"tool_response":   map[string]string{"content": knownErr + "\n" + strings.Repeat("x", 1200<<10)},
		"session_id":      "other-tool",
		"cwd":             "/work/app",
	}
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runHookToolAfter(dir, bytes.NewReader(b), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("a cut payload from a non-command tool got answered:\n%q", out.String())
	}
}
