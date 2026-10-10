package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Each wall's text is recovered from its newest session, and those are
// different sessions. One pass over the record log has to serve all of them:
// a pass per wall walked a 200 MB log six times on a session start.
func TestTopFrictionReadsTheLogOnceForSeveralWalls(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	walls := []string{
		"make: *** [widget] Error 2",
		"zsh:1: command not found: frobnicate",
		"fatal: refusing to merge unrelated histories",
	}
	for w, wall := range walls {
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("w%ds%d", w, i)
			// Minutes apart so every wall has its own newest session.
			at := fmt.Sprintf("2026-07-01T1%d:%02d:00Z", w, i*10)
			body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` + at + `","message":{"role":"user","content":"the build keeps stopping"}}` + "\n" +
				`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","content":"` + wall + `"}]}}` + "\n"
			p := filepath.Join(root, "-w-app", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	before := RecordLogScans()
	got := TopFriction(dir, 0, nil)
	if scans := RecordLogScans() - before; scans != 1 {
		t.Fatalf("recovering %d walls took %d passes over the record log, want 1", len(got), scans)
	}
	if len(got) != len(walls) {
		t.Fatalf("got %d walls, want %d: %+v", len(got), len(walls), got)
	}
	seen := map[string]bool{}
	for _, f := range got {
		seen[f.Text] = true
	}
	for _, wall := range walls {
		line, _ := FrictionLine(wall)
		if !seen[line] {
			t.Fatalf("wall %q missing from %+v", line, got)
		}
	}
}

// The batched read hands back what reading the session on its own did.
func TestFrictionReaderMatchesASessionRead(t *testing.T) {
	dir := frictionIgnoreStore(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	rd := newFrictionReader(dir, m)
	for _, meta := range m.Sessions {
		rd.ahead = append(rd.ahead, meta)
	}
	for _, meta := range m.Sessions {
		got, ok := rd.toolOutput(meta)
		want, wok := newFrictionReader(dir, m).loadOne(meta)
		if ok != wok || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: batched %v %q, alone %v %q", meta.ID, ok, got, wok, want)
		}
	}
}
