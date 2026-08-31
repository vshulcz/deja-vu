package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The other half of the sweep that framed `how` (#2844): `fix` hands an agent a
// command to run at the moment it has just hit an error, which is the moment it
// is most likely to run it without reading. It arrived with no frame and no
// note — the same gap, on the sharper surface.
func TestTheFixToolFramesTheCommandItServes(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const errLine = "ld: symbol(s) not found for architecture arm64"
	// Twice, in two sessions: one sighting is held back as unconfirmed.
	for i, sid := range []string{"s1", "s2"} {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + string(rune('1'+i)) + `T09:00:00Z","message":{"role":"user","content":"build is broken again"}}
{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + string(rune('1'+i)) + `T09:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + errLine + `"}]}}
{"type":"assistant","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + string(rune('1'+i)) + `T09:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go build -ldflags=-w ./..."}}]}}
`
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"`+errLine+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "go build -ldflags=-w") {
		t.Fatalf("no command was served, so there is nothing to frame:\n%s", text)
	}
	if !strings.Contains(text, "untrusted reference data") {
		t.Errorf("a command to run arrives with nothing saying where it came from:\n%s", text)
	}
}
