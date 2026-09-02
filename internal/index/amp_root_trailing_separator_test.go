package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestAmpTrailingRootSeparatorMatchesDiscoveredFile(t *testing.T) {
	root := t.TempDir()
	ampRoot := filepath.Join(root, "amp", "threads")
	t.Setenv("DEJA_AMP_ROOT", ampRoot+string(filepath.Separator))

	path := filepath.Join(ampRoot, "thread-1.json")
	if err := os.MkdirAll(ampRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = `{"id":"thread-1","title":"Trailing root","created":1767337445000,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	files := sources.AmpThreadFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("AmpThreadFiles = %v, want [%s]", files, path)
	}
	discovered := files[0]
	if got := harnessForPath(discovered); got != "amp" {
		t.Fatalf("harnessForPath(%q) = %q, want amp", discovered, got)
	}
	sessions, err := parseChangedFile("", discovered, FileState{})
	if err != nil {
		t.Fatalf("parseChangedFile(%q): %v", discovered, err)
	}
	if len(sessions) != 1 || sessions[0].ID != "thread-1" {
		t.Fatalf("parseChangedFile(%q) = %#v, want thread-1 session", discovered, sessions)
	}
}
