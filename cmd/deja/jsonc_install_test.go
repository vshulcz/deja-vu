package main

import (
	"encoding/json"
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
// the stripped text is an offset into the original — and it leaves strings
// alone, or a config holding an https:// URL loses the rest of that line and
// is refused again (#2740).
func TestStrippingCommentsBlanksCommentsAndNothingElse(t *testing.T) {
	// The strings deja must not touch, byte for byte.
	for _, in := range []string{
		`{"url": "https://example.com/x", "a": 1}`,
		`{"a": "// not a comment"}`,
		`{"a": "/* nor this */"}`,
	} {
		if got := stripJSONComments(in); got != in {
			t.Errorf("a string was blanked:\n%q\n%q", in, got)
		}
	}
	// And the comments, which go without moving anything else.
	for _, in := range []string{
		"{\n  // one\n  \"a\": 1\n}\n",
		"{\n  /* two */ \"a\": 1\n}\n",
		"{\n  \"a\": 1 // trailing\n}\n",
		"{\n  /* over\n     two lines */\n  \"a\": 1\n}\n",
	} {
		got := stripJSONComments(in)
		if len(got) != len(in) {
			t.Errorf("length changed: %q -> %q", in, got)
		}
		if strings.Contains(got, "//") || strings.Contains(got, "/*") {
			t.Errorf("a comment survived: %q -> %q", in, got)
		}
		var v any
		if err := json.Unmarshal([]byte(got), &v); err != nil {
			t.Errorf("what is left is not JSON: %v\n%q", err, got)
		}
		for i := range in {
			if in[i] == '\n' && got[i] != '\n' {
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

// An empty block took a comma it had nothing to separate, which is not JSON —
// and the file was then refused on every later run, with a message pointing at
// the reader's comment rather than at deja's own byte (#2740).
func TestAnEmptyBlockDoesNotGetADanglingComma(t *testing.T) {
	for _, before := range []string{
		"{\n  // mine\n  \"mcpServers\": {}\n}\n",
		"{\n  // mine\n  \"mcpServers\": {\n  }\n}\n",
	} {
		hermeticEnv(t)
		path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
			t.Errorf("the file deja wrote is not JSON: %v\n%s", err, b)
		}
		// And a second run still reads it.
		if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
			t.Errorf("the file deja wrote refused the next install: %v\n%s", err, b)
		}
	}
}

// A block that is not an object is a config deja does not understand. Writing
// a second key of the same name leaves the reader's value winning and deja
// unwired, with "updated" printed over it (#2740, the shape #2399 settled).
func TestANonObjectBlockIsRefusedNotDuplicated(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"mcpServers\": null\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err == nil {
		b, _ := os.ReadFile(path)
		t.Errorf("a block deja cannot edit was accepted:\n%s", b)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the file was rewritten anyway:\n%s", b)
	}
}

// The entry deja finds is merged onto, not replaced: an env pointing at a
// store on another disk and a `disabled` the reader set are theirs (#2479).
func TestACommentedConfigKeepsTheFieldsOnDejasOwnEntry(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{
  // mine
  "mcpServers": {
    "deja": {"command": "/old/deja", "args": ["mcp"], "env": {"DEJA_INDEX_DIR": "/big/disk"}, "disabled": true}
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "cursor", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "/big/disk") {
		t.Errorf("the reader's env was dropped:\n%s", got)
	}
	if !strings.Contains(got, `"disabled": true`) {
		t.Errorf("a disabled entry was silently switched on:\n%s", got)
	}
	if !strings.Contains(out, "left the entry disabled") {
		t.Errorf("nothing said the entry is still off:\n%s", out)
	}
}

// And an entry deja wrote under another name is the entry it takes over, not
// one to write a second server beside (#2269, #2712).
func TestACommentedConfigDoesNotGetASecondDejaEntry(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"mcpServers\": {\n    \"deja-vu\": {\"command\": \"/old/bin/deja\", \"args\": [\"mcp\"]}\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file is not JSON: %v\n%s", err, b)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Errorf("%d servers after the install, want the one deja took over:\n%s", len(servers), b)
	}
}
