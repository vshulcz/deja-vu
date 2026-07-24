package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

func viewFixtureIndex(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-app", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","timestamp":"` + ts + `","message":{"role":"user","content":"<local-command-caveat>plumbing</local-command-caveat>"}}`,
		`{"type":"user","sessionId":"one","timestamp":"` + ts + `","message":{"role":"user","content":"real question about </script> escaping"}}`,
		`{"type":"user","sessionId":"one","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"text","text":"real answer"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestViewGeneratesSelfContainedHTML(t *testing.T) {
	dir := viewFixtureIndex(t)
	out := filepath.Join(t.TempDir(), "view.html")
	path, err := writeViewHTML(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"deja view", "Sessions", "Recalls", "Notes", "real question about"} {
		if !strings.Contains(s, want) {
			t.Fatalf("view missing %q", want)
		}
	}
	// Self-contained: no external fetches of any kind.
	if regexp.MustCompile(`(src|href)="https?://`).MatchString(s) {
		t.Fatal("view references external assets")
	}
	// Script-closer inside indexed text must not break out of the data block.
	if strings.Contains(s, "about </script>") {
		t.Fatal("unescaped script closer leaked into the page")
	}
	// Plumbing wrappers stay out of previews.
	if strings.Contains(s, "local-command-caveat") {
		t.Fatal("plumbing leaked into preview")
	}
}

func TestViewPreviewCap(t *testing.T) {
	long := strings.Repeat("word ", viewPreviewBytes)
	got := sessionPreview([]model.Message{{Role: "user", Text: long}})
	if len(got) > viewPreviewBytes+8 {
		t.Fatalf("preview exceeds cap: %d", len(got))
	}
}
