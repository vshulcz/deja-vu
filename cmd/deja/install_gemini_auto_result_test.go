package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The gemini-auto target writes two things — the MCP entry and the hooks
// extension — and its result has to say so, the way every other -auto target
// folds its writes: an uninstall that removed the extension and left
// hooksConfig on said neither (#3185).
func TestGeminiAutoResultNamesTheExtension(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"GitHub"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := installTarget("gemini-auto", "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in.Path+" "+in.Note, "extensions") {
		t.Errorf("install did not name the extension: path=%q note=%q", in.Path, in.Note)
	}
	out, err := installTarget("gemini-auto", "/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Path+" "+out.Note, "extensions") || !strings.Contains(out.Note, "hooksConfig") {
		t.Errorf("uninstall did not name the extension or the switch it left on: path=%q note=%q", out.Path, out.Note)
	}
}
