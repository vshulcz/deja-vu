package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAmpDiscoveryAndIncrementalIndexParsing(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	setHome(t, home)
	t.Setenv("DEJA_AMP_ROOT", filepath.Join(root, "amp", "threads"))
	path := filepath.Join(os.Getenv("DEJA_AMP_ROOT"), "thread-index.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"id":"thread-index","title":"Index Amp","created":1767337445000,"env":{"initial":{"trees":[{"uri":"file:///tmp/index-project"}]}},"messages":[{"role":"user","content":[{"type":"text","text":"index needle"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	files := currentFiles("amp")
	if _, ok := files[path]; !ok {
		t.Fatalf("currentFiles(amp) omitted %q: %v", path, files)
	}
	if got := harnessForPath(path); got != "amp" {
		t.Fatalf("harnessForPath = %q, want amp", got)
	}
	sessions, err := parseChangedFile("amp", path, FileState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Harness != "amp" || sessions[0].Project != "index-project" {
		t.Fatalf("parseChangedFile = %#v", sessions)
	}
}
