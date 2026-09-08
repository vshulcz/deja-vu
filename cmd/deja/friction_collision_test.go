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

// Two failures of one service differ only in how it failed — refused, or reset
// mid-read — and everything that says which is past the 79 bytes a row holds.
// The list then printed the same line twice, each with its own count, and the
// reader had no way to tell them apart (#3401).
func TestFrictionRowsThatShareTheirHeadStillDiffer(t *testing.T) {
	root := frictionEnv(t)
	const shared = `Get "http://toy-load-toy-load.default.svc.cluster.local/work?cpu_ms=20&jitter_ms=5": `
	lines := []string{
		shared + "dial tcp 0.0.0.0:0->192.0.2.73:80: connect: connection refused",
		shared + "read tcp 192.0.2.35:39473->192.0.2.73:80: read: connection reset by peer",
	}
	if len(shared) <= 79 {
		t.Fatalf("the shared head is %d bytes, so the rows never collide", len(shared))
	}
	proj := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, line := range lines {
		for i := 0; i < index.FrictionMinSessions; i++ {
			sid := fmt.Sprintf("s%d%02d", n, i)
			payload, err := json.Marshal(line + "\nexit status 1")
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

	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "sessions  ") {
			rows = append(rows, strings.TrimSpace(l))
		}
	}
	if len(rows) != 2 {
		t.Fatalf("want the two failures as two rows, got %d:\n%s", len(rows), buf.String())
	}
	if rows[0] == rows[1] {
		t.Errorf("both failures print as %q — the reader sees one error twice", rows[0])
	}
	for _, r := range rows {
		if len(r) > 79+len("   5 sessions  ") {
			t.Errorf("row is %d bytes, past what a terminal row holds: %q", len(r), r)
		}
		if !strings.Contains(r, "toy-load") {
			t.Errorf("row no longer names the failing service: %q", r)
		}
	}
}
