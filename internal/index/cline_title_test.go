package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// `cline history update --title` rewrites the <id>.json manifest and leaves
// the transcript alone, and only a changed transcript marked the session for
// a re-read, so the old title stayed (#4319).
func TestClineManifestRenameIsIndexed(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "cline-sessions")
	t.Setenv("DEJA_CLINE_ROOT", root)

	id := "1790000000000_abcde"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, id+".json")
	write := func(title string) {
		t.Helper()
		body := `{"sessionId":"` + id + `","cwd":"/tmp/proj","metadata":{"title":"` + title + `"}}`
		if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("run the tests")
	transcript := `{"version":1,"messages":[{"role":"user","content":[{"type":"text","text":"clinetitleneedle run the tests"}],"ts":1767225600000}]}`
	if err := os.WriteFile(filepath.Join(dir, id+".messages.json"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(tmp, "index.db")
	opts := search.Options{Query: "clinetitleneedle", Harness: "cline"}
	if err := EnsureForSearch(indexDir, opts, false, nil); err != nil {
		t.Fatal(err)
	}
	recent, err := Recent(indexDir, 1)
	if err != nil || len(recent) != 1 || recent[0].Title != "run the tests" {
		t.Fatalf("bad indexed title: %#v err=%v", recent, err)
	}

	// Same size, so only the mtime says the manifest changed.
	write("renamed: pytest")
	tick := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(manifest, tick, tick); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(indexDir, opts, false, nil); err != nil {
		t.Fatal(err)
	}
	recent, err = Recent(indexDir, 1)
	if err != nil || len(recent) != 1 || recent[0].Title != "renamed: pytest" {
		t.Fatalf("manifest-only rename was not indexed: %#v err=%v", recent, err)
	}
}

// The state has to follow the manifest the reader opens, which is named after
// the session directory whatever the transcript beside it is called.
func TestClineMetadataFileIsTheReadersManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("DEJA_CLINE_ROOT", root)
	dir := filepath.Join(root, "1790000000000_abcde")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"sessionId":"1790000000000_abcde"}`
	if err := os.WriteFile(filepath.Join(dir, "1790000000000_abcde.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1790000000000_abcde.messages.json", "teammate.messages.json"} {
		k, ok := kindForPath(filepath.Join(dir, name))
		if !ok || k.Sidecar == nil {
			t.Fatalf("%s: no sidecar for the cline-sdk kind", name)
		}
		if size, _ := k.Sidecar(filepath.Join(dir, name)); size != int64(len(manifest)) {
			t.Errorf("%s: sidecar size %d, want the manifest's %d", name, size, len(manifest))
		}
	}
}
