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

// The view page is a self-contained file made to be passed around, so it must
// carry the same redaction floor the other outbound paths print when it holds a
// masked secret — and stay quiet when it does not, so local browsing is silent
// (#857).
func TestViewNamesTheRedactionFloorWhenThePageHoldsAMaskedSecret(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-app", "sec.jsonl"), "sec", []string{
		`{"type":"user","sessionId":"sec","timestamp":"` + ts + `","message":{"role":"user","content":"the key was AKIAABCDABCDABCDABCD in the config"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "v.html")
	stderr, err := captureRunStderr(t, "view", "--no-open", "--out", out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "pattern redaction is a floor") {
		t.Fatalf("view did not name the redaction floor for a page carrying a masked secret:\n%s", stderr)
	}
}

func TestViewStaysQuietWhenNothingIsMasked(t *testing.T) {
	viewFixtureIndex(t)
	out := filepath.Join(t.TempDir(), "v.html")
	stderr, err := captureRunStderr(t, "view", "--no-open", "--out", out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "pattern redaction is a floor") {
		t.Fatalf("view printed the floor on a page with no masked content:\n%s", stderr)
	}
}

func TestViewGeneratesSelfContainedHTML(t *testing.T) {
	dir := viewFixtureIndex(t)
	out := filepath.Join(t.TempDir(), "view.html")
	path, _, err := writeViewHTML(dir, out)
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
	got := clipForPage(sessionPreview([]model.Message{{Role: "user", Text: long}}))
	if len(got) > viewPreviewBytes+8 {
		t.Fatalf("preview exceeds cap: %d", len(got))
	}
}

// Same as the stats card: the view handed back the bare syscall when its
// target sat on a disk that is gone (#1036).
func TestViewSaysWhatIsWrongWithThePath(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_, _, err := writeViewHTML(dir, filepath.Join(tmp, "no-such-dir", "m.html"))
	if err == nil {
		t.Fatal("writing into a directory that does not exist did not fail")
	}
	if !strings.Contains(err.Error(), "its directory does not exist") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
	if strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("the syscall is still handed back: %v", err)
	}
}
