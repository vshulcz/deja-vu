package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With no "mcp" block yet, install adds one above the file's closing brace and
// puts a comma on the last key before it. It found that key by walking up past
// every line already ending in a comma, so a config whose last key had a
// trailing comma got the comma on the next line up — in a nested config an
// opening brace, `"shim": {,` — and Kilo and opencode refused the file (#4399).
func TestInstallJSONCRootTrailingCommaKeepsTheFileParsing(t *testing.T) {
	cases := map[string]string{
		"the reported kilo.jsonc": `{
  // shim provider for the hunt
  "$schema": "https://app.kilo.ai/config.json",
  "provider": {
    "shim": {
      "npm": "@ai-sdk/openai-compatible",
      "options": { "baseURL": "http://127.0.0.1:47962/v1", "apiKey": "x" },
      "models": { "luna": { "name": "luna" } },
    },
  },
  "model": "shim/luna",
}`,
		"comments between the last key and the brace": `{
  "provider": {
    "shim": {
      "models": { "luna": {} },
    },
  },
  "model": "shim/luna",
  // the model above is the shim's
  /* and so is
     the provider */
}`,
		"trailing comma then a comment on the same line": `{
  "provider": {
    "shim": {"npm": "x"},
  },
  "model": "shim/luna", // shim
}`,
		"no comma, comment on the line": `{
  "provider": {
    "shim": {"npm": "x"}
  },
  "model": "shim/luna" // shim
}`,
		"last key closes a nested block with a comma": `{
  "provider": {
    "shim": {"npm": "x"},
  },
}`,
		"last key closes a nested block without one": `{
  "provider": {
    "shim": {"npm": "x"},
  }
}`,
	}
	for _, base := range []string{"kilo", "opencode"} {
		for name, in := range cases {
			t.Run(base+"/"+name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, base+".jsonc")
				if err := os.WriteFile(path, []byte(in+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := installOpencodeShaped(dir, base, "/usr/local/bin/deja", false); err != nil {
					t.Fatal(err)
				}
				got := readTestFile(t, path)
				if err := parsesAsJSONC(t, got); err != nil {
					t.Fatalf("install left a file that does not parse: %v\n%s", err, got)
				}
				if strings.Contains(got, "{,") {
					t.Fatalf("a comma landed on an opening brace:\n%s", got)
				}
				if strings.Contains(got, ",,") {
					t.Fatalf("a second comma landed on a line that had one:\n%s", got)
				}
				if !strings.Contains(got, `"deja"`) {
					t.Fatalf("deja's entry is missing:\n%s", got)
				}
				// Whatever the reader wrote stays where it was: every one of
				// their lines is still in the file, at most with a comma added.
				noCommas := strings.ReplaceAll(got, ",", "")
				for _, l := range strings.Split(in, "\n") {
					if !strings.Contains(noCommas, strings.ReplaceAll(l, ",", "")) {
						t.Errorf("line %q is gone or changed:\n%s", l, got)
					}
				}
				if _, err := installOpencodeShaped(dir, base, "/usr/local/bin/deja", true); err != nil {
					t.Fatal(err)
				}
				back := readTestFile(t, path)
				if err := parsesAsJSONC(t, back); err != nil {
					t.Fatalf("uninstall left a file that does not parse: %v\n%s", err, back)
				}
				if strings.Contains(back, "{,") || strings.Contains(back, `"deja"`) {
					t.Fatalf("uninstall left deja's marks behind:\n%s", back)
				}
			})
		}
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A CRLF config: the comma deja adds belongs before the carriage return. After
// it, the line ends in a bare CR, which an editor shows as a line break with
// the comma alone at the start of the next line.
func TestInstallJSONCCommaLandsBeforeTheCarriageReturn(t *testing.T) {
	out, _, err := updateOpencodeJSONC([]byte("{\r\n  \"model\": \"shim/luna\"\r\n}\r\n"), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\"model\": \"shim/luna\",\r\n") {
		t.Errorf("the comma is not at the end of the code:\n%q", out)
	}
}
