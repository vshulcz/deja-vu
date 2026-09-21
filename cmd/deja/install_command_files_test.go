package main

import (
	"os"
	"strings"
	"testing"
)

// Commands are not part of the Agent Skills standard, so every harness has its
// own directory and its own file format. A command written in the wrong shape
// is not rejected — it is simply never offered when someone types "/".
func TestCommandFilesMatchEachHarnessShape(t *testing.T) {
	hermeticEnv(t)
	// A Windows path is run through as well: it is the shape that broke the
	// TOML file Gemini used to get, and the markdown ones have to survive it
	// too.
	for _, exe := range []string{"/bin/deja", `C:\Users\dev\deja.exe`} {
		for _, h := range []string{"opencode", "cursor", "roo", "kilocode"} {
			r, err := installCommandFile(h, exe, false)
			if err != nil || r.Path == "" {
				t.Fatalf("%s command = %#v, %v", h, r, err)
			}
			b, err := os.ReadFile(r.Path)
			if err != nil {
				t.Fatalf("%s: %v", h, err)
			}
			body := string(b)
			if !strings.Contains(body, exe) {
				t.Errorf("%s command does not name the binary:\n%s", h, body)
			}
			if h == "cursor" {
				// Cursor reads the description off the first line and shows
				// `---` for a file that opens with frontmatter — measured in
				// its own palette against two project commands (#3666).
				if strings.HasPrefix(body, "---\n") {
					t.Errorf("cursor command opens with frontmatter, which it shows as its description:\n%s", body)
				}
				if !strings.HasPrefix(body, "Search this machine's past AI coding sessions") {
					t.Errorf("cursor command does not open with its description:\n%s", body)
				}
			} else if !strings.HasPrefix(body, "---\n") || !strings.Contains(body, "description:") {
				t.Errorf("%s command has no markdown frontmatter:\n%s", h, body)
			}
			if !strings.Contains(body, "$ARGUMENTS") {
				t.Errorf("%s command has no $ARGUMENTS placeholder:\n%s", h, body)
			}

			// Uninstall takes back exactly what was written.
			if r, err = installCommandFile(h, exe, true); err != nil || r.Action != "removed" {
				t.Fatalf("%s uninstall = %#v, %v", h, r, err)
			}
			if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
				t.Errorf("%s command survived uninstall", h)
			}
		}
	}
}
