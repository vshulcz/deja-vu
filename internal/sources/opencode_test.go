package sources

import (
	"strings"
	"testing"
)

func TestPatchSpansTakesTheRemovedLines(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: /w/app.go\n" +
		"@@\n" +
		"-func old() error {\n" +
		"-\treturn nil\n" +
		"+func nw() error {\n" +
		"+\treturn errors.New(\"x\")\n" +
		"*** End Patch"
	got := patchSpans(patch)
	if len(got) != 1 {
		t.Fatalf("got %d spans, want one", len(got))
	}
	path, body, _ := strings.Cut(got[0], "\n")
	if path != "/w/app.go" {
		t.Fatalf("path = %q", path)
	}
	if body != "func old() error {\n\treturn nil" {
		t.Fatalf("body = %q — only the removed lines belong in a span", body)
	}
}

func TestPatchSpansHandlesSeveralFilesAndNoRemovals(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: /w/a.go\n@@\n-one\n+two\n" +
		"*** Add File: /w/b.go\n+brand new\n" +
		"*** Update File: /w/c.go\n@@\n-three\n" +
		"*** End Patch"
	got := patchSpans(patch)
	if len(got) != 2 {
		t.Fatalf("got %v — a file with nothing removed has no span to keep", got)
	}
	if !strings.HasPrefix(got[0], "/w/a.go\n") || !strings.HasPrefix(got[1], "/w/c.go\n") {
		t.Fatalf("got %v", got)
	}
	if len(patchSpans("")) != 0 || len(patchSpans("nonsense")) != 0 {
		t.Fatal("junk in, nothing out")
	}
}

func TestWorthIndexingCoversMoreThanOneEcosystem(t *testing.T) {
	for _, cmd := range []string{
		"go test ./internal/index/ -run Posting",
		"git status --short",
		"latexmk -pdf thesis.tex",
		"uv run pytest -q",
		"gradle build",
		"psql -c 'select 1'",
	} {
		if !worthIndexing(cmd) {
			t.Errorf("%q is work worth recording", cmd)
		}
	}
	for _, cmd := range []string{
		"ls -la",
		"cd /tmp",
		"cat file.txt",
		"python3 - <<'PY'\nprint(1)\nPY",
	} {
		if worthIndexing(cmd) {
			t.Errorf("%q is not", cmd)
		}
	}
}
