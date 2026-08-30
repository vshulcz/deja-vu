package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A `//` line is not a broken config. Five writers decoded the file as strict
// JSON and refused the whole target over one comment, so somebody who had
// annotated their config could not install deja at all — while Zed's config
// has been edited as text since #1285 precisely so comments survive (#1664).
func TestInstallKeepsACommentedConfig(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{
  // the server I wrote myself
  "mcpServers": {
    "mine": {"command": "/usr/local/bin/mine"}
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "install", "cursor", "--no-index")
	if err != nil {
		t.Fatalf("a comment refused the target: %v\n%s", err, out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "// the server I wrote myself") {
		t.Errorf("the reader's comment was dropped:\n%s", got)
	}
	if !strings.Contains(got, `"mine": {"command": "/usr/local/bin/mine"}`) {
		t.Errorf("the reader's own entry was rewritten:\n%s", got)
	}
	if !strings.Contains(got, `"deja"`) {
		t.Errorf("deja's entry was not written:\n%s", got)
	}

	// A second install changes nothing, and an uninstall gives the file back.
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != got {
		t.Errorf("a second install rewrote the file:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), "// the server I wrote myself") ||
		!strings.Contains(string(back), `"mine"`) {
		t.Errorf("the uninstall did not give the file back:\n%s", back)
	}
	if strings.Contains(string(back), `"deja"`) {
		t.Errorf("deja's entry survived the uninstall:\n%s", back)
	}
}

// A file that is broken some other way is still refused: deja does not guess
// at what a reader meant to write.
func TestABrokenConfigIsStillRefused(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers": {`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err == nil {
		t.Error("a truncated config was accepted")
	}
}

// The comment scanner keeps every other byte where it was, so an offset into
// the stripped text is an offset into the original.
func TestStrippingCommentsKeepsEveryOtherByteInPlace(t *testing.T) {
	for _, in := range []string{
		"{\n  // one\n  \"a\": 1\n}\n",
		"{\n  /* two\n     lines */\n  \"a\": 1\n}\n",
		`{"a": "// not a comment", "b": "/* nor this */"}`,
		"{\n  \"a\": 1 // trailing\n}\n",
	} {
		out := stripJSONComments(in)
		if len(out) != len(in) {
			t.Errorf("length changed: %q -> %q", in, out)
		}
		for i := range in {
			if in[i] == '\n' && out[i] != '\n' {
				t.Errorf("a newline moved at %d in %q", i, in)
			}
		}
	}
	if !configIsJSONC([]byte("{\n // c\n \"a\": 1\n}")) {
		t.Error("a commented object was not read as JSONC")
	}
	if configIsJSONC([]byte(`{"a": 1}`)) {
		t.Error("plain JSON was read as JSONC")
	}
	if configIsJSONC([]byte(`{"a": `)) {
		t.Error("a truncated object was read as JSONC")
	}
}
