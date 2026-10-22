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

// The JSON row carries the recorded error, not the terminal's view of it.
// trimFriction bounds a line to 79 bytes because that is what fits a row; a
// consumer has no row, and a clipped error can no longer be matched against
// the error it came from or handed back to `deja fix`.
func TestFrictionJSONCarriesTheWholeErrorLine(t *testing.T) {
	root := frictionEnv(t)
	// With an escape in it, so the row is pinned as sanitised as well as whole.
	long := "psql: error: \x1b[31mconnection\x1b[0m to server at \"db-primary-replica-eu-central.internal.example.com\" (192.0.2.14), port 5432 failed: Connection refused"
	if len(long) <= 79 {
		t.Fatalf("the fixture is %d bytes, so it never reaches the cut", len(long))
	}
	proj := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range index.FrictionMinSessions {
		sid := fmt.Sprintf("long%02d", i)
		payload, err := json.Marshal(long + "\nexit status 2")
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

	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), []string{"--json"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got frictionJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Rows) == 0 {
		t.Fatalf("nothing was reported, so this asserts nothing:\n%s", buf.String())
	}
	if !strings.Contains(got.Rows[0].Error, "Connection refused") {
		t.Errorf("the row was cut before the end of the error:\n%q", got.Rows[0].Error)
	}
	if strings.HasSuffix(got.Rows[0].Error, "…") {
		t.Errorf("the row carries the terminal's ellipsis:\n%q", got.Rows[0].Error)
	}
	if strings.Contains(got.Rows[0].Error, "\x1b[") {
		t.Errorf("an ANSI escape reached the row:\n%q", got.Rows[0].Error)
	}
}

// The prose path keeps its bound: a terminal row is still a terminal row.
func TestFrictionProseStillFitsARow(t *testing.T) {
	long := strings.Repeat("connection refused on the replica ", 6)
	if got := trimFriction(long); len(got) > 79 {
		t.Errorf("the prose line is %d bytes, over the row bound", len(got))
	}
}
